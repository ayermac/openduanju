package biz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// A manual work is declared by an operator instead of being imported from a
// provider. It therefore carries no source code and no external identifier,
// and it enters exactly the same draft, upload, processing, review and
// publication path as an imported work.

// Bounds of the manual creation form. They are product rules, not storage
// limits: the same numbers gate the browser form.
const (
	ManualTitleLimit       = 120
	ManualDescriptionLimit = 5000
	ManualEpisodeLimit     = 500
	ManualEpisodeNoMax     = 9999
	ManualRequestIDLimit   = 64
)

// Manual series failures.
var (
	ErrManualTitleRequired = errors.New("the work title is required and must fit the limit")
	ErrManualDescription   = errors.New("the description is too long")
	ErrManualEpisodes      = errors.New("a manual work needs at least one episode")
	ErrManualEpisodeNo     = errors.New("an episode number is out of range")
	ErrDuplicateEpisodeNo  = errors.New("an episode number appears twice")
	ErrIdempotencyConflict = errors.New("the request id was used for another payload")
)

// ManualEpisodeInput is one episode the operator declares. It carries only the
// original number: the work has no provider identity, and the number is kept
// as written instead of being replaced by the position in the list.
type ManualEpisodeInput struct {
	Number int
}

// ManualSeriesInput is the whole creation form.
type ManualSeriesInput struct {
	Title       string
	Description string
	Episodes    []ManualEpisodeInput
	// RequestID is the idempotency key of the form. A repeated submission with
	// the same key returns the series that already exists.
	RequestID string
	// Actor is the staff account that submitted the form.
	Actor string
}

// Normalize trims the text, checks the bounds and refuses a repeated episode
// number. It never drops an episode: the operator decides what the work holds.
func (in ManualSeriesInput) Normalize() (ManualSeriesInput, error) {
	out := ManualSeriesInput{
		Title:       strings.TrimSpace(in.Title),
		Description: strings.TrimSpace(in.Description),
		RequestID:   strings.TrimSpace(in.RequestID),
		Actor:       in.Actor,
		Episodes:    make([]ManualEpisodeInput, 0, len(in.Episodes)),
	}
	if out.Title == "" || utf8.RuneCountInString(out.Title) > ManualTitleLimit {
		return ManualSeriesInput{}, fmt.Errorf("%w: 1~%d characters", ErrManualTitleRequired, ManualTitleLimit)
	}
	if utf8.RuneCountInString(out.Description) > ManualDescriptionLimit {
		return ManualSeriesInput{}, fmt.Errorf("%w: at most %d characters", ErrManualDescription, ManualDescriptionLimit)
	}
	if utf8.RuneCountInString(out.RequestID) > ManualRequestIDLimit {
		return ManualSeriesInput{}, fmt.Errorf("%w: the request id is too long", ErrIdempotencyConflict)
	}
	if len(in.Episodes) == 0 || len(in.Episodes) > ManualEpisodeLimit {
		return ManualSeriesInput{}, fmt.Errorf("%w: at most %d episodes", ErrManualEpisodes, ManualEpisodeLimit)
	}
	seen := make(map[int]bool, len(in.Episodes))
	for _, episode := range in.Episodes {
		if episode.Number < 1 || episode.Number > ManualEpisodeNoMax {
			return ManualSeriesInput{}, fmt.Errorf("%w: %d", ErrManualEpisodeNo, episode.Number)
		}
		if seen[episode.Number] {
			return ManualSeriesInput{}, fmt.Errorf("%w: %d", ErrDuplicateEpisodeNo, episode.Number)
		}
		seen[episode.Number] = true
		out.Episodes = append(out.Episodes, ManualEpisodeInput{Number: episode.Number})
	}
	return out, nil
}

// Fingerprint identifies the payload a request id was used for. A retry of the
// same form matches; the same id with another payload is a conflict instead of
// a silent reuse of the wrong work.
func (in ManualSeriesInput) Fingerprint() string {
	parts := make([]string, 0, len(in.Episodes)+3)
	parts = append(parts, "manual", in.Title, in.Description)
	for _, episode := range in.Episodes {
		parts = append(parts, fmt.Sprint(episode.Number))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// CreateManualSeries declares a local work with its first draft. Nothing is
// downloaded, processed or reviewed here: the draft is only the declaration.
//
// The work and the record of who created it are committed together. A
// repeated submission of the same form changes nothing and is not recorded as
// a second creation.
func (uc *SourceCatalog) CreateManualSeries(ctx context.Context, input ManualSeriesInput) (view *SeriesView, err error) {
	ctx, scope := uc.audit.Begin(ctx, AuditActionSeriesCreate, AuditResourceSeries)
	defer func() { scope.End(ctx, err) }()
	normalized, err := input.Normalize()
	if err != nil {
		return nil, err
	}
	return uc.repo.CreateManualSeries(ctx, normalized, normalized.Fingerprint())
}
