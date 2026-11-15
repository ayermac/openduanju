package data

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"openduanju-server/internal/biz"
)

// FFmpegConfig holds the processor tunables. Paths are executable names or
// absolute paths; they are never built from user input.
type FFmpegConfig struct {
	FFmpegPath   string
	FFprobePath  string
	Preset       string
	CRF          string
	AudioBitrate string
	Timeout      time.Duration
}

// FFmpegProcessor probes and normalizes media with the ffmpeg toolchain.
// Subprocesses run with a deadline and a separate process group; nothing is
// executed through a shell.
type FFmpegProcessor struct {
	cfg    FFmpegConfig
	logger *slog.Logger
}

// NewFFmpegProcessor validates the tool configuration.
func NewFFmpegProcessor(cfg FFmpegConfig, logger *slog.Logger) (*FFmpegProcessor, error) {
	if strings.TrimSpace(cfg.FFmpegPath) == "" || strings.TrimSpace(cfg.FFprobePath) == "" {
		return nil, biz.NewMediaError(biz.MediaStageValidate, biz.MediaCodeConfigInvalid, false, errors.New("ffmpeg/ffprobe path is required"))
	}
	if cfg.Preset == "" {
		cfg.Preset = "veryfast"
	}
	if cfg.CRF == "" {
		cfg.CRF = "20"
	}
	if cfg.AudioBitrate == "" {
		cfg.AudioBitrate = "128k"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Minute
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &FFmpegProcessor{cfg: cfg, logger: logger}, nil
}

type probeDocument struct {
	Format struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
		Size       string `json:"size"`
		BitRate    string `json:"bit_rate"`
	} `json:"format"`
	Streams []struct {
		CodecType   string `json:"codec_type"`
		CodecName   string `json:"codec_name"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
		Duration    string `json:"duration"`
		BitRate     string `json:"bit_rate"`
		Disposition struct {
			AttachedPic int `json:"attached_pic"`
		} `json:"disposition"`
	} `json:"streams"`
}

// Probe reads container and codec facts with ffprobe.
func (p *FFmpegProcessor) Probe(ctx context.Context, path string) (*biz.MediaInfo, error) {
	if strings.TrimSpace(path) == "" {
		return nil, biz.NewMediaError(biz.MediaStageValidate, biz.MediaCodeConfigInvalid, false, errors.New("probe path is empty"))
	}
	if _, err := os.Stat(path); err != nil {
		return nil, biz.NewMediaError(biz.MediaStageValidate, biz.MediaCodeMediaNotFound, false, err)
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout(p.cfg.Timeout))
	defer cancel()
	cmd := exec.CommandContext(probeCtx, p.cfg.FFprobePath,
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout, stderr boundedBuffer
	stdout.limit = 4 << 20
	stderr.limit = 8 << 10
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if probeCtx.Err() != nil {
		return nil, biz.NewMediaError(biz.MediaStageValidate, biz.MediaCodeProcessTimeout, true, probeCtx.Err())
	}
	if err != nil {
		return nil, biz.NewMediaError(biz.MediaStageValidate, biz.MediaCodeMediaInvalid, false,
			fmt.Errorf("ffprobe failed: %w: %s", err, stderr.tail()))
	}
	var doc probeDocument
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		return nil, biz.NewMediaError(biz.MediaStageValidate, biz.MediaCodeMediaInvalid, false, fmt.Errorf("decode ffprobe output: %w", err))
	}
	return mediaInfoFromProbe(&doc), nil
}

func mediaInfoFromProbe(doc *probeDocument) *biz.MediaInfo {
	info := &biz.MediaInfo{
		Container: doc.Format.FormatName,
	}
	info.DurationMS = secondsToMillis(doc.Format.Duration)
	info.SizeBytes = parsePositiveInt(doc.Format.Size)
	info.VideoBitrate = parsePositiveInt(doc.Format.BitRate)
	for _, stream := range doc.Streams {
		switch stream.CodecType {
		case "video":
			if stream.Disposition.AttachedPic != 0 || info.VideoCodec != "" {
				continue
			}
			info.VideoCodec = stream.CodecName
			info.Width = stream.Width
			info.Height = stream.Height
			if ms := secondsToMillis(stream.Duration); ms > 0 {
				info.DurationMS = ms
			}
			if bitrate := parsePositiveInt(stream.BitRate); bitrate > 0 {
				info.VideoBitrate = bitrate
			}
		case "audio":
			if info.AudioCodec == "" {
				info.AudioCodec = stream.CodecName
			}
		}
	}
	return info
}

// Prepare normalizes one input into the fixed delivery profile. Already
// compatible streams are remuxed; everything else is transcoded.
func (p *FFmpegProcessor) Prepare(ctx context.Context, req biz.ProcessRequest) (*biz.ProcessedMedia, error) {
	if strings.TrimSpace(req.InputPath) == "" || strings.TrimSpace(req.OutputDir) == "" {
		return nil, biz.NewMediaError(biz.MediaStageProcess, biz.MediaCodeConfigInvalid, false, errors.New("input path and output directory are required"))
	}
	if err := os.MkdirAll(req.OutputDir, 0o750); err != nil {
		return nil, biz.NewMediaError(biz.MediaStageProcess, biz.MediaCodeProcessFailed, false, fmt.Errorf("create output directory: %w", err))
	}
	inputInfo, err := p.Probe(ctx, req.InputPath)
	if err != nil {
		return nil, err
	}
	if !inputInfo.Playable() {
		return nil, biz.NewMediaError(biz.MediaStageValidate, biz.MediaCodeMediaInvalid, false,
			fmt.Errorf("input has no playable video track (codec=%q duration=%dms)", inputInfo.VideoCodec, inputInfo.DurationMS))
	}
	videoCopy := strings.EqualFold(inputInfo.VideoCodec, "h264")
	audioCopy := inputInfo.AudioCodec == "" || strings.EqualFold(inputInfo.AudioCodec, "aac")
	outputName, err := randomFileName()
	if err != nil {
		return nil, biz.NewMediaError(biz.MediaStageProcess, biz.MediaCodeProcessFailed, false, err)
	}
	outputPath := filepath.Join(req.OutputDir, outputName)

	args := []string{
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-i", req.InputPath,
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-sn", "-dn",
		"-map_metadata", "-1",
	}
	if videoCopy {
		args = append(args, "-c:v", "copy")
		if audioCopy {
			args = append(args, "-c:a", "copy")
		} else {
			args = append(args, "-c:a", "aac", "-b:a", p.cfg.AudioBitrate)
		}
	} else {
		args = append(args,
			"-c:v", "libx264",
			"-preset", p.cfg.Preset,
			"-crf", p.cfg.CRF,
			"-pix_fmt", "yuv420p",
			"-c:a", "aac",
			"-b:a", p.cfg.AudioBitrate,
		)
	}
	if inputInfo.AudioCodec == "" {
		args = append(args, "-an")
	}
	args = append(args, "-movflags", "+faststart", "-f", "mp4", outputPath)

	processCtx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()
	cmd := exec.CommandContext(processCtx, p.cfg.FFmpegPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
	var stderr boundedBuffer
	stderr.limit = 16 << 10
	cmd.Stderr = &stderr
	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start)
	if processCtx.Err() != nil {
		_ = os.Remove(outputPath)
		if errors.Is(processCtx.Err(), context.DeadlineExceeded) {
			return nil, biz.NewMediaError(biz.MediaStageProcess, biz.MediaCodeProcessTimeout, true,
				fmt.Errorf("ffmpeg exceeded %s: %s", p.cfg.Timeout, stderr.tail()))
		}
		return nil, biz.NewMediaError(biz.MediaStageProcess, biz.MediaCodeCanceled, false, context.Canceled)
	}
	if runErr != nil {
		_ = os.Remove(outputPath)
		return nil, biz.NewMediaError(biz.MediaStageProcess, biz.MediaCodeProcessFailed, false,
			fmt.Errorf("ffmpeg failed: %w: %s", runErr, stderr.tail()))
	}
	info, err := os.Stat(outputPath)
	if err != nil || info.Size() <= 0 {
		_ = os.Remove(outputPath)
		return nil, biz.NewMediaError(biz.MediaStageProcess, biz.MediaCodeProcessFailed, true, fmt.Errorf("ffmpeg produced no output"))
	}
	sum, md5Base64, err := fileDigests(outputPath)
	if err != nil {
		_ = os.Remove(outputPath)
		return nil, biz.NewMediaError(biz.MediaStageProcess, biz.MediaCodeProcessFailed, true, err)
	}
	outputInfo, err := p.Probe(ctx, outputPath)
	if err != nil {
		_ = os.Remove(outputPath)
		return nil, err
	}
	if !strings.EqualFold(outputInfo.VideoCodec, "h264") {
		_ = os.Remove(outputPath)
		return nil, biz.NewMediaError(biz.MediaStageProcess, biz.MediaCodeMediaUnsupported, false,
			fmt.Errorf("output video codec is %q", outputInfo.VideoCodec))
	}
	if outputInfo.AudioCodec != "" && !strings.EqualFold(outputInfo.AudioCodec, "aac") {
		_ = os.Remove(outputPath)
		return nil, biz.NewMediaError(biz.MediaStageProcess, biz.MediaCodeMediaUnsupported, false,
			fmt.Errorf("output audio codec is %q", outputInfo.AudioCodec))
	}
	p.logger.Info("media processed",
		"profile", req.ProfileKey,
		"remuxed", videoCopy && audioCopy,
		"elapsed_ms", elapsed.Milliseconds(),
		"bytes", info.Size(),
		"duration_ms", outputInfo.DurationMS,
		"width", outputInfo.Width,
		"height", outputInfo.Height,
	)
	return &biz.ProcessedMedia{
		Path:       outputPath,
		SHA256:     sum,
		MD5Base64:  md5Base64,
		SizeBytes:  info.Size(),
		Info:       outputInfo,
		Remuxed:    videoCopy && audioCopy,
		ProfileKey: req.ProfileKey,
	}, nil
}

func probeTimeout(processTimeout time.Duration) time.Duration {
	if processTimeout <= 0 {
		return time.Minute
	}
	if processTimeout < time.Minute {
		return processTimeout
	}
	return time.Minute
}

func secondsToMillis(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	return int64(seconds * 1000)
}

func parsePositiveInt(value string) int64 {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

func randomFileName() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("random file name: %w", err)
	}
	return hex.EncodeToString(buf) + ".mp4", nil
}

// fileDigests computes the content identity (SHA-256) and the transport
// checksum (base64 MD5) in one pass over the processed file.
func fileDigests(path string) (string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	sha := sha256.New()
	rawMD5 := md5.New()
	if _, err := io.Copy(io.MultiWriter(sha, rawMD5), file); err != nil {
		return "", "", err
	}
	return hex.EncodeToString(sha.Sum(nil)), base64.StdEncoding.EncodeToString(rawMD5.Sum(nil)), nil
}

// boundedBuffer keeps at most limit bytes and retains the most recent ones.
type boundedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return len(p), nil
	}
	b.buf.Write(p)
	if b.buf.Len() > b.limit {
		over := b.buf.Len() - b.limit
		data := b.buf.Bytes()
		b.buf.Reset()
		b.buf.Write(data[over:])
		b.truncated = true
	}
	return len(p), nil
}

func (b *boundedBuffer) Bytes() []byte { return b.buf.Bytes() }

func (b *boundedBuffer) tail() string {
	text := strings.TrimSpace(b.buf.String())
	if text == "" {
		return "no stderr"
	}
	if len(text) > 512 {
		text = text[len(text)-512:]
	}
	return text
}
