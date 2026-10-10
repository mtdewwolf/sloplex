package transcode

import (
	"errors"
	"io"
)

const tsPacket = 188

// FirstPTS returns the presentation timestamp (90 kHz) of the first video
// frame of an MPEG-TS file, or the earliest PES timestamp if it has no
// video. It anchors the X-TIMESTAMP-MAP of HLS WebVTT subtitles: ffmpeg's
// segments do not start at 0, and audio may start a little before video,
// while subtitle time 0 is the first picture.
func FirstPTS(r io.Reader) (int64, error) {
	buf := make([]byte, 1<<20)
	n, err := io.ReadFull(r, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return 0, err
	}
	buf = buf[:n]
	seen := map[int]bool{}
	best := int64(-1)
	video := int64(-1)
	for off := 0; off+tsPacket <= len(buf); off += tsPacket {
		p := buf[off : off+tsPacket]
		if p[0] != 0x47 {
			return 0, errors.New("mpegts: lost sync")
		}
		pusi := p[1]&0x40 != 0
		pid := int(p[1]&0x1f)<<8 | int(p[2])
		afc := p[3] >> 4 & 3
		if !pusi || afc&1 == 0 || seen[pid] {
			continue
		}
		start := 4
		if afc&2 != 0 {
			start += 1 + int(p[4])
		}
		if start+14 > tsPacket {
			continue
		}
		pes := p[start:]
		if pes[0] != 0 || pes[1] != 0 || pes[2] != 1 || pes[7]&0x80 == 0 {
			continue // PSI table, or a PES packet without a PTS
		}
		seen[pid] = true
		pts := int64(pes[9]>>1&0x07)<<30 | int64(pes[10])<<22 | int64(pes[11]>>1)<<15 | int64(pes[12])<<7 | int64(pes[13]>>1)
		if pes[3]&0xF0 == 0xE0 && video < 0 { // stream IDs 0xE0-0xEF are video
			video = pts
		}
		if best < 0 || pts < best {
			best = pts
		}
	}
	if video >= 0 {
		return video, nil
	}
	if best < 0 {
		return 0, errors.New("mpegts: no timestamp found")
	}
	return best, nil
}
