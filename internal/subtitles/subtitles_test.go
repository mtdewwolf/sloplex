package subtitles

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShiftVTT(t *testing.T) {
	in := "WEBVTT\n\nNOTE kept\n\n1\n00:00:01.000 --> 00:00:03.000\nGone\n\n2\n00:00:04.500 --> 00:00:06.000 align:start\nPartly\n\n3\n01:00:10.000 --> 01:00:12.250\nLater\n"
	got := string(ShiftVTT([]byte(in), 5*time.Second))
	for _, want := range []string{"WEBVTT", "NOTE kept", "00:00:00.000 --> 00:00:01.000 align:start\nPartly", "01:00:05.000 --> 01:00:07.250\nLater"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Gone") {
		t.Errorf("cue ending before 0 kept:\n%s", got)
	}
	if string(ShiftVTT([]byte(in), 0)) != in {
		t.Error("zero offset changed the document")
	}
}

func TestFormats(t *testing.T) {
	if !IsText("subrip") || !IsText("mov_text") || IsText("hdmv_pgs_subtitle") || IsText("dvd_subtitle") {
		t.Error("IsText")
	}
	if NativeFormat("ssa") != ASS || NativeFormat("mov_text") != SRT || NativeFormat("webvtt") != VTT {
		t.Error("NativeFormat")
	}
	if !ValidFormat("vtt") || ValidFormat("sub") || ContentType(VTT) != "text/vtt; charset=utf-8" {
		t.Error("formats")
	}
}

func TestExtractAndConvert(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	srt := filepath.Join(dir, "en.srt")
	if err := os.WriteFile(srt, []byte("1\n00:00:01,000 --> 00:00:02,500\nCaf\xe9 au lait\n"), 0o644); err != nil { // Windows-1252 é
		t.Fatal(err)
	}
	video := filepath.Join(dir, "v.mkv")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "color=black:s=64x64:d=3", "-sub_charenc", "CP1252", "-i", srt,
		"-map", "0", "-map", "1", "-c:v", "libx264", "-c:s", "srt", video).CombinedOutput(); err != nil {
		t.Skipf("cannot build test video: %v %s", err, out)
	}
	s := New(ffmpeg, filepath.Join(dir, "cache"))
	ctx := context.Background()

	vtt, err := s.Convert(ctx, srt, SRT, VTT)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(vtt)
	if !strings.HasPrefix(string(b), "WEBVTT") || !strings.Contains(string(b), "Café au lait") || !strings.Contains(string(b), "00:01.000 --> 00:02.500") {
		t.Errorf("converted VTT:\n%s", b)
	}
	if same, _ := s.Convert(ctx, srt, SRT, SRT); same != srt {
		t.Error("same-format conversion should return the source")
	}

	ex, err := s.Extract(ctx, video, 1, 5, 1, "subrip")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(ex, ".srt") {
		t.Errorf("extract path %s", ex)
	}
	b, _ = os.ReadFile(ex)
	if !strings.Contains(string(b), "Café au lait") {
		t.Errorf("extracted:\n%s", b)
	}
	if _, err := New("", dir).Extract(ctx, video, 1, 5, 1, "subrip"); err != ErrUnavailable {
		t.Errorf("no ffmpeg = %v", err)
	}
}
