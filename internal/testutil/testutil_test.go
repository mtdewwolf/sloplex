package testutil

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAverageLumaCropSize(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	// A uniform gray image must have the same mean for every crop size.
	// On ffmpeg 7.1.5, scale=1:1:flags=area reports 237 rather than 219
	// for the 300x300 crop, even though every decoded pixel is 219.
	const luma = 219
	file := filepath.Join(t.TempDir(), "uniform.pgm")
	data := append([]byte("P5\n640 360\n255\n"), bytes.Repeat([]byte{luma}, 640*360)...)
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, crop := range []string{"140:140:10:10", "300:300:10:10", "640:360:0:0"} {
		t.Run(crop, func(t *testing.T) {
			if got := AverageLuma(t, ffmpeg, file, "0", crop); got != luma {
				t.Errorf("uniform crop mean = %d, want %d", got, luma)
			}
		})
	}
}
