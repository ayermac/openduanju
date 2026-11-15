package data_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data"
)

func requireFFmpeg(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
}

func newFFmpegProcessor(t *testing.T, timeout time.Duration) *data.FFmpegProcessor {
	t.Helper()
	processor, err := data.NewFFmpegProcessor(data.FFmpegConfig{
		FFmpegPath:   "ffmpeg",
		FFprobePath:  "ffprobe",
		Preset:       "ultrafast",
		CRF:          "28",
		AudioBitrate: "96k",
		Timeout:      timeout,
	}, nil)
	if err != nil {
		t.Fatalf("NewFFmpegProcessor: %v", err)
	}
	return processor
}

func generateCompatibleMP4(t *testing.T, dir string) string {
	t.Helper()
	requireFFmpeg(t)
	path := filepath.Join(dir, "compatible.mp4")
	cmd := exec.Command("ffmpeg",
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100",
		"-t", "2",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-movflags", "+faststart",
		path,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate mp4: %v: %s", err, out)
	}
	return path
}

func generateIncompatibleAVI(t *testing.T, dir string) string {
	t.Helper()
	requireFFmpeg(t)
	path := filepath.Join(dir, "incompatible.avi")
	cmd := exec.Command("ffmpeg",
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100",
		"-t", "2",
		"-c:v", "mpeg4", "-c:a", "pcm_s16le",
		path,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate avi: %v: %s", err, out)
	}
	return path
}

func TestFFmpegProbeReportsPlayableVideo(t *testing.T) {
	dir := t.TempDir()
	input := generateCompatibleMP4(t, dir)
	processor := newFFmpegProcessor(t, time.Minute)

	info, err := processor.Probe(context.Background(), input)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !info.Playable() {
		t.Fatalf("info is not playable: %+v", info)
	}
	if info.VideoCodec != "h264" || info.AudioCodec != "aac" {
		t.Fatalf("codecs = %s/%s, want h264/aac", info.VideoCodec, info.AudioCodec)
	}
	if info.Width != 320 || info.Height != 240 {
		t.Fatalf("size = %dx%d, want 320x240", info.Width, info.Height)
	}
	if info.DurationMS < 1500 || info.DurationMS > 2500 {
		t.Fatalf("duration = %dms, want about 2000ms", info.DurationMS)
	}
}

func TestFFmpegPrepareRemuxesCompatibleInput(t *testing.T) {
	dir := t.TempDir()
	input := generateCompatibleMP4(t, dir)
	processor := newFFmpegProcessor(t, time.Minute)

	processed, err := processor.Prepare(context.Background(), biz.ProcessRequest{
		InputPath:  input,
		OutputDir:  filepath.Join(dir, "out"),
		ProfileKey: "web-mp4-h264-aac-v1",
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !processed.Remuxed {
		t.Fatal("compatible input must be remuxed, not transcoded")
	}
	assertProcessedOutput(t, processed)
}

func TestFFmpegPrepareTranscodesIncompatibleInput(t *testing.T) {
	dir := t.TempDir()
	input := generateIncompatibleAVI(t, dir)
	processor := newFFmpegProcessor(t, time.Minute)

	processed, err := processor.Prepare(context.Background(), biz.ProcessRequest{
		InputPath:  input,
		OutputDir:  filepath.Join(dir, "out"),
		ProfileKey: "web-mp4-h264-aac-v1",
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if processed.Remuxed {
		t.Fatal("mpeg4/pcm input must be transcoded")
	}
	assertProcessedOutput(t, processed)
}

func assertProcessedOutput(t *testing.T, processed *biz.ProcessedMedia) {
	t.Helper()
	if processed.Info == nil || !processed.Info.Playable() {
		t.Fatalf("output is not playable: %+v", processed.Info)
	}
	if processed.Info.VideoCodec != "h264" || processed.Info.AudioCodec != "aac" {
		t.Fatalf("output codecs = %s/%s, want h264/aac", processed.Info.VideoCodec, processed.Info.AudioCodec)
	}
	if len(processed.SHA256) != 64 {
		t.Fatalf("sha256 = %q", processed.SHA256)
	}
	info, err := os.Stat(processed.Path)
	if err != nil || info.Size() != processed.SizeBytes || info.Size() == 0 {
		t.Fatalf("output file stat mismatch: %v size=%d want=%d", err, infoSize(info), processed.SizeBytes)
	}
	if filepath.Ext(processed.Path) != ".mp4" {
		t.Fatalf("output extension = %q", filepath.Ext(processed.Path))
	}
	if !strings.Contains(processed.Info.Container, "mp4") {
		t.Fatalf("output container = %q, want mp4", processed.Info.Container)
	}
}

func infoSize(info os.FileInfo) int64 {
	if info == nil {
		return 0
	}
	return info.Size()
}

func TestFFmpegProbeRejectsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.mp4")
	if err := os.WriteFile(path, []byte("this is not a video"), 0o600); err != nil {
		t.Fatal(err)
	}
	processor := newFFmpegProcessor(t, time.Minute)
	_, err := processor.Probe(context.Background(), path)
	code, ok := biz.MediaErrorCodeOf(err)
	if !ok || code != biz.MediaCodeMediaInvalid {
		t.Fatalf("code = %s (%v), want %s", code, err, biz.MediaCodeMediaInvalid)
	}
}

func TestFFmpegPrepareStopsAtTimeout(t *testing.T) {
	dir := t.TempDir()
	input := generateCompatibleMP4(t, dir)
	fake := filepath.Join(dir, "slow-ffmpeg.sh")
	script := "#!/bin/sh\nsleep 5\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	processor, err := data.NewFFmpegProcessor(data.FFmpegConfig{
		FFmpegPath:   fake,
		FFprobePath:  "ffprobe",
		Preset:       "ultrafast",
		CRF:          "28",
		AudioBitrate: "96k",
		Timeout:      300 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = processor.Prepare(context.Background(), biz.ProcessRequest{
		InputPath:  input,
		OutputDir:  filepath.Join(dir, "out"),
		ProfileKey: "web-mp4-h264-aac-v1",
	})
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeProcessTimeout {
		t.Fatalf("code = %s (%v), want %s", code, err, biz.MediaCodeProcessTimeout)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("timeout took %s, process was not killed", elapsed)
	}
}

func TestFFmpegPrepareDoesNotUseShell(t *testing.T) {
	dir := t.TempDir()
	source := generateCompatibleMP4(t, dir)
	injected := filepath.Join(dir, "clip;touch pwned.mp4")
	if err := os.Rename(source, injected); err != nil {
		t.Fatal(err)
	}
	processor := newFFmpegProcessor(t, time.Minute)
	if _, err := processor.Prepare(context.Background(), biz.ProcessRequest{
		InputPath:  injected,
		OutputDir:  filepath.Join(dir, "out"),
		ProfileKey: "web-mp4-h264-aac-v1",
	}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatal("input file name reached a shell")
	}
}
