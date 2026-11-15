package biz

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image"
	// Register the decoders a cover may use. The registration is what makes
	// a real decode possible; the file extension is never consulted.
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
)

// CoverImageInfo is the verified shape of a cover image.
type CoverImageInfo struct {
	ContentType string
	Extension   string
	Width       int
	Height      int
}

// validateCoverFile proves that the file is a real image before it may be
// bound to a revision. The declared content type and the file name are not
// evidence: the bytes are sniffed and decoded.
func validateCoverFile(path string, opts UploadOptions) (*CoverImageInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCoverImageInvalid, err)
	}
	if info.IsDir() || info.Size() <= 0 {
		return nil, fmt.Errorf("%w: the file is empty", ErrCoverImageInvalid)
	}
	if opts.MaxCoverBytes > 0 && info.Size() > opts.MaxCoverBytes {
		return nil, fmt.Errorf("%w: limit is %d bytes", ErrUploadTooLarge, opts.MaxCoverBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCoverImageInvalid, err)
	}
	defer func() { _ = file.Close() }()

	sniff := make([]byte, 512)
	n, err := io.ReadFull(file, sniff)
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, fmt.Errorf("%w: %v", ErrCoverImageInvalid, err)
	}
	detected := http.DetectContentType(sniff[:n])
	var extension string
	switch detected {
	case "image/jpeg":
		extension = ".jpg"
	case "image/png":
		extension = ".png"
	default:
		// A video renamed to .jpg, a script, or an unsupported container is
		// rejected here and never stored as a cover.
		return nil, fmt.Errorf("%w: detected %s", ErrCoverImageUnsupported, detected)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCoverImageInvalid, err)
	}
	config, format, err := image.DecodeConfig(file)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCoverImageInvalid, err)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return nil, fmt.Errorf("%w: no image dimensions", ErrCoverImageInvalid)
	}
	if opts.MinCoverSide > 0 && (config.Width < opts.MinCoverSide || config.Height < opts.MinCoverSide) {
		return nil, fmt.Errorf("%w: %dx%d is smaller than %dpx", ErrCoverImageInvalid, config.Width, config.Height, opts.MinCoverSide)
	}
	if opts.MaxCoverSide > 0 && (config.Width > opts.MaxCoverSide || config.Height > opts.MaxCoverSide) {
		return nil, fmt.Errorf("%w: %dx%d is larger than %dpx", ErrCoverImageInvalid, config.Width, config.Height, opts.MaxCoverSide)
	}
	switch format {
	case "jpeg", "png":
	default:
		return nil, fmt.Errorf("%w: decoded as %s", ErrCoverImageUnsupported, format)
	}
	return &CoverImageInfo{
		ContentType: detected,
		Extension:   extension,
		Width:       config.Width,
		Height:      config.Height,
	}, nil
}

// randomToken returns a hex token for an unguessable staging key.
func randomToken(size int) string {
	if size <= 0 {
		size = 16
	}
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failing is not recoverable for a key that must be
		// unguessable; the caller must not continue with a weak key.
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(buf)
}

// removeFile deletes a temporary upload copy and ignores a missing file.
func removeFile(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path)
}
