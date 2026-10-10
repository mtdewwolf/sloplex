// Package testutil holds helpers shared by tests: a PGS subtitle generator
// and a frame brightness probe.
package testutil

import (
	"bytes"
	"encoding/binary"
	"image/jpeg"
	"os"
	"os/exec"
	"strconv"
	"testing"
)

// PGS (Blu-ray "presentation graphics") segment types, from the publicly
// documented .sup format.
const (
	pgsPDS = 0x14 // palette definition
	pgsODS = 0x15 // object (bitmap) definition
	pgsPCS = 0x16 // presentation composition
	pgsWDS = 0x17 // window definition
	pgsEND = 0x80
)

type supWriter struct{ bytes.Buffer }

func (w *supWriter) segment(pts uint32, typ byte, payload []byte) {
	w.WriteString("PG")
	binary.Write(w, binary.BigEndian, pts)
	binary.Write(w, binary.BigEndian, uint32(0)) // DTS
	w.WriteByte(typ)
	binary.Write(w, binary.BigEndian, uint16(len(payload)))
	w.Write(payload)
}

func be16(v int) []byte { return []byte{byte(v >> 8), byte(v)} }

// WriteSUP writes a PGS subtitle that shows a solid white w×h box at (x, y)
// on a frameW×frameH canvas from startMs to endMs.
func WriteSUP(t testing.TB, path string, frameW, frameH, x, y, w, h, startMs, endMs int) {
	t.Helper()
	var s supWriter
	window := append([]byte{1, 0}, append(append(be16(x), be16(y)...), append(be16(w), be16(h)...)...)...)
	pcs := func(state byte, objects int) []byte {
		b := append(be16(frameW), be16(frameH)...)
		b = append(b, 0x10)                   // frame rate
		b = append(b, be16(int(state)>>7)...) // composition number
		b = append(b, state, 0, 0, byte(objects))
		if objects > 0 {
			b = append(b, 0, 0, 0, 0) // object 0, window 0, not cropped
			b = append(b, append(be16(x), be16(y)...)...)
		}
		return b
	}

	// Run-length encoded bitmap: every line is one run of palette entry 1.
	var rle []byte
	for range h {
		if w < 64 {
			rle = append(rle, 0, 0x80|byte(w), 1)
		} else {
			rle = append(rle, 0, 0xC0|byte(w>>8), byte(w), 1)
		}
		rle = append(rle, 0, 0) // end of line
	}
	n := len(rle) + 4
	ods := []byte{0, 0, 0, 0xC0, byte(n >> 16), byte(n >> 8), byte(n)}
	ods = append(ods, append(be16(w), be16(h)...)...)
	ods = append(ods, rle...)

	show := uint32(startMs * 90)
	s.segment(show, pgsPCS, pcs(0x80, 1)) // epoch start with one object
	s.segment(show, pgsWDS, window)
	s.segment(show, pgsPDS, []byte{0, 0, 0, 16, 128, 128, 0, 1, 235, 128, 128, 255}) // 0 transparent, 1 white
	s.segment(show, pgsODS, ods)
	s.segment(show, pgsEND, nil)
	hide := uint32(endMs * 90)
	s.segment(hide, pgsPCS, pcs(0x00, 0)) // no objects: clear
	s.segment(hide, pgsWDS, window)
	s.segment(hide, pgsEND, nil)
	if err := os.WriteFile(path, s.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// BrightPixels counts near-white pixels in the frame at time t of an MP4.
func BrightPixels(t testing.TB, ffmpeg, mp4 string, at string) int {
	t.Helper()
	out, err := exec.Command(ffmpeg, "-v", "error", "-ss", at, "-i", mp4, "-frames:v", "1", "-f", "image2pipe", "-c:v", "mjpeg", "pipe:1").Output()
	if err != nil {
		t.Fatalf("frame at %s: %v", at, err)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r > 0xc000 && g > 0xc000 && bl > 0xc000 {
				n++
			}
		}
	}
	return n
}

// AverageLuma returns the mean luma (0-255, full range) of a crop
// ("w:h:x:y") of the frame at time at.
func AverageLuma(t testing.TB, ffmpeg, file, at, crop string) int {
	t.Helper()
	out, err := exec.Command(ffmpeg, "-v", "error", "-ss", at, "-i", file, "-frames:v", "1",
		"-vf", "crop="+crop+",format=gray", "-f", "rawvideo", "pipe:1").Output()
	if err != nil || len(out) == 0 {
		t.Fatalf("average luma of %s at %s: %v (%d bytes)", file, at, err, len(out))
	}
	// Average the decoded pixels directly. Scaling a large crop to 1x1
	// can change even a uniform image's luma on some ffmpeg versions.
	var sum uint64
	for _, v := range out {
		sum += uint64(v)
	}
	return int((sum + uint64(len(out))/2) / uint64(len(out)))
}

// WriteHDRClip encodes a 10-bit HEVC clip tagged with the given transfer
// (smpte2084 or arib-std-b67) whose left half has luma code leftCode and
// right half rightCode (10-bit limited range). It skips the test when
// libx265 is unavailable.
func WriteHDRClip(t testing.TB, ffmpeg, path, transfer string, leftCode, rightCode int) {
	t.Helper()
	geq := "color=c=black:s=640x360:d=2:r=10,format=yuv420p10le,geq=lum='if(lt(X,320)," +
		strconv.Itoa(leftCode) + "," + strconv.Itoa(rightCode) + ")':cb=512:cr=512"
	out, err := exec.Command(ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", geq,
		"-c:v", "libx265", "-pix_fmt", "yuv420p10le",
		"-x265-params", "colorprim=bt2020:transfer="+transfer+":colormatrix=bt2020nc:log-level=error",
		"-color_primaries", "bt2020", "-color_trc", transfer, "-colorspace", "bt2020nc", path).CombinedOutput()
	if err != nil {
		t.Skipf("cannot encode an HDR clip (libx265 missing?): %v %s", err, out)
	}
}
