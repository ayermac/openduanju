package biz

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// SourceErrorCode is a stable failure reason for web metadata collection.
type SourceErrorCode string

// Classified source failure codes.
const (
	SourceCodeConfigInvalid SourceErrorCode = "SOURCE_CONFIG_INVALID"
	SourceCodeUnavailable   SourceErrorCode = "SOURCE_UNAVAILABLE"
	SourceCodeRejected      SourceErrorCode = "SOURCE_REJECTED"
	SourceCodeRateLimited   SourceErrorCode = "SOURCE_RATE_LIMITED"
	SourceCodeNotFound      SourceErrorCode = "SOURCE_NOT_FOUND"
	SourceCodeParseFailed   SourceErrorCode = "SOURCE_PARSE_FAILED"
	SourceCodeBudget        SourceErrorCode = "SOURCE_BUDGET_EXCEEDED"
)

// SourceError is a classified metadata-adapter failure. Err is for logs only.
type SourceError struct {
	Code      SourceErrorCode
	Retryable bool
	Err       error
}

func (e *SourceError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err == nil {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Err)
}

func (e *SourceError) Unwrap() error { return e.Err }

// NewSourceError builds a classified source error.
func NewSourceError(code SourceErrorCode, retryable bool, err error) *SourceError {
	return &SourceError{Code: code, Retryable: retryable, Err: err}
}

// SourceErrorCodeOf extracts the stable code from err.
func SourceErrorCodeOf(err error) (SourceErrorCode, bool) {
	var se *SourceError
	if errors.As(err, &se) {
		return se.Code, true
	}
	return "", false
}

// SourceRetryable reports whether err can be retried later.
func SourceRetryable(err error) bool {
	var se *SourceError
	if errors.As(err, &se) {
		return se.Retryable
	}
	return false
}

// ListSeriesRequest selects one ranking channel and page. Origin is the
// channel's configured origin; an empty value keeps the adapter's own.
type ListSeriesRequest struct {
	Channel string
	Page    int
	Origin  string
}

// SeriesSummary is one ranking entry with its source identity.
type SeriesSummary struct {
	ExternalID       string
	Title            string
	CoverURL         string
	Description      string
	Rank             int
	Tags             []string
	EpisodeCount     int
	OpenEpisodeCount int
	// Metrics keeps the source's own text (heat/score/favorites) verbatim.
	Metrics map[string]string
}

// SeriesPage is one bounded page of rankings.
type SeriesPage struct {
	Channel    string
	Page       int
	TotalPages int
	SourceURL  string
	FetchedAt  time.Time
	Items      []SeriesSummary
	// Dropped counts entries skipped for missing/unsafe identity.
	Dropped int
}

// EpisodeRef is the real external identity of one episode. Number is the
// source's original episode number, not a local selection index.
type EpisodeRef struct {
	ExternalID string
	Number     int
}

// SeriesDetail is the source's series metadata and full episode identity list.
type SeriesDetail struct {
	ExternalID      string
	Title           string
	CoverURL        string
	Description     string
	Tags            []string
	EpisodeCount    int
	AccessibleCount int
	SourceURL       string
	FetchedAt       time.Time
	Episodes        []EpisodeRef
}

// WebMetadataAdapter discovers series and episode identities from the source
// web pages. It never returns playback URLs or protected material.
type WebMetadataAdapter interface {
	Key() string
	// Entries declares the entry points this adapter can really execute.
	Entries() []SourceEntry
	ListSeries(ctx context.Context, req ListSeriesRequest) (*SeriesPage, error)
	GetSeries(ctx context.Context, externalID string) (*SeriesDetail, error)
}
