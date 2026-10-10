package transcode

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"time"

	"github.com/mtdewwolf/sloplex/internal/probe"
)

// probe640x360 describes the lavfi test pattern: video only, not H.264, so
// it is always encoded.
var probe640x360 = probe.Info{VideoCodec: "rawvideo", Width: 640, Height: 360}

// DefaultDevice is the DRM render node used by VAAPI and QSV.
const DefaultDevice = "/dev/dri/renderD128"

// Hwaccel config values.
const (
	HwaccelNone = "none"
	HwaccelAuto = "auto"
)

// ValidHwaccel reports whether v is an accepted hwaccel config value.
func ValidHwaccel(v string) bool {
	switch v {
	case HwaccelNone, HwaccelAuto, AccelVAAPI, AccelQSV, AccelNVENC, AccelVideoToolbox:
		return true
	}
	return false
}

// candidates lists what "auto" tries on each OS, in order.
func candidates(requested, goos string) []string {
	switch requested {
	case HwaccelNone, "":
		return nil
	case HwaccelAuto:
		if goos == "darwin" {
			return []string{AccelVideoToolbox}
		}
		if goos == "linux" {
			return []string{AccelNVENC, AccelQSV, AccelVAAPI}
		}
		return nil
	}
	return []string{requested}
}

// Detect picks the hardware accelerator to use by test-encoding a short
// pattern with each candidate. It returns a zero HW (software) when nothing
// works; see "Hardware acceleration" in docs/spec/transcode.md.
func Detect(ctx context.Context, ffmpeg, requested, device, goos string, log *slog.Logger) HW {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if device == "" {
		device = DefaultDevice
	}
	tried := candidates(requested, goos)
	for _, accel := range tried {
		if err := testEncode(ctx, ffmpeg, accel, device); err != nil {
			log.Debug("hardware encoder unavailable", "hwaccel", accel, "err", err)
			continue
		}
		hw := HW{Accel: accel, Device: device}
		log.Info("hardware transcoding enabled", "hwaccel", accel, "encoder", Encoder(accel), "device", device)
		return hw
	}
	if len(tried) > 0 {
		log.Warn("no working hardware encoder; transcoding in software", "requested", requested, "tried", tried)
	}
	return HW{}
}

// testEncode encodes a tenth of a second of test pattern with the
// encode-only pipeline of an accelerator, using the same argument builder
// as real transcodes.
func testEncode(ctx context.Context, ffmpeg, accel, device string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	r := Request{Input: "testsrc=size=640x360:rate=10:duration=0.5", InputFormat: "lavfi",
		Source: probe640x360, MaxWidth: 320, MaxHeight: 180}
	p := Decide(r)
	pl := Pipeline{Accel: accel, Device: device, Full: accel == AccelVideoToolbox}
	cmd := exec.CommandContext(ctx, ffmpeg, Args(r, p, Output{Null: true}, pl)...)
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4 << 10}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return nil
}
