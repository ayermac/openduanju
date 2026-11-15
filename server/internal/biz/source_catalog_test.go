package biz_test

import (
	"context"
	"errors"
	"testing"

	"openduanju-server/internal/biz"
)

type stubCatalogRepo struct {
	biz.SourceCatalogRepo
}

// GetChannelByCode answers the channel a plan names, so plan validation can be
// exercised without a database.
func (stubCatalogRepo) GetChannelByCode(_ context.Context, sourceCode, channelCode string) (*biz.ChannelConfig, error) {
	return &biz.ChannelConfig{
		SourceCode: sourceCode, Code: channelCode, EntryCode: channelCode,
		RatePerMinute: 30, Status: biz.SourceStatusEnabled, SourceStatus: biz.SourceStatusEnabled,
	}, nil
}

type stubAdapter struct {
	biz.WebMetadataAdapter
}

func (stubAdapter) Key() string { return "stub_web" }

func (stubAdapter) Entries() []biz.SourceEntry {
	return []biz.SourceEntry{{AdapterKey: "stub_web", Kind: "rank", Channel: "hot-drama"}}
}

func newValidationCatalog(t *testing.T) *biz.SourceCatalog {
	t.Helper()
	uc, err := biz.NewSourceCatalog(&stubCatalogRepo{}, &stubAdapter{}, nil, nil)
	if err != nil {
		t.Fatalf("NewSourceCatalog: %v", err)
	}
	return uc
}

func TestSourceCatalogRejectsInvalidPlan(t *testing.T) {
	uc := newValidationCatalog(t)
	base := biz.IngestionPlan{
		Code: "p", Name: "计划", SourceCode: "hongguo", Channel: "hot-drama",
		PageStart: 1, PageEnd: 1, RequestBudget: 10, ItemBudget: 10,
	}
	if _, err := uc.CreatePlan(context.Background(), biz.IngestionPlan{Code: "p"}); !errors.Is(err, biz.ErrPlanPageRange) {
		t.Fatalf("missing fields error = %v", err)
	}
	tooMany := base
	tooMany.PageEnd = 6
	if _, err := uc.CreatePlan(context.Background(), tooMany); !errors.Is(err, biz.ErrPlanPageRange) {
		t.Fatalf("page span error = %v", err)
	}
	badBudget := base
	badBudget.RequestBudget = 0
	if _, err := uc.CreatePlan(context.Background(), badBudget); !errors.Is(err, biz.ErrPlanBudget) {
		t.Fatalf("budget error = %v", err)
	}
}

func TestSourceCatalogRejectsInvalidSelection(t *testing.T) {
	uc := newValidationCatalog(t)
	if _, err := uc.SetSelection(context.Background(), 1, 1, "explicit", []string{"a", "a"}); !errors.Is(err, biz.ErrSelectionMismatch) {
		t.Fatalf("duplicate ids error = %v", err)
	}
	if _, err := uc.SetSelection(context.Background(), 1, 1, "explicit", []string{""}); !errors.Is(err, biz.ErrSelectionMismatch) {
		t.Fatalf("empty id error = %v", err)
	}
	if _, err := uc.SetSelection(context.Background(), 1, 1, "all", []string{"a"}); !errors.Is(err, biz.ErrSelectionMismatch) {
		t.Fatalf("all mode with ids error = %v", err)
	}
	if _, err := uc.SetSelection(context.Background(), 1, 1, "surprise", nil); !errors.Is(err, biz.ErrSelectionMismatch) {
		t.Fatalf("unknown mode error = %v", err)
	}
	if _, err := uc.SetSelection(context.Background(), 0, 1, "all", nil); !errors.Is(err, biz.ErrRevisionNotFound) {
		t.Fatalf("missing revision error = %v", err)
	}
}

func TestCandidateHashIsStable(t *testing.T) {
	summary := biz.SeriesSummary{ExternalID: "1", Title: "剧", Tags: []string{"a"}, EpisodeCount: 2}
	first := biz.CandidateHash(summary)
	summary.Title = "剧2"
	if first == biz.CandidateHash(summary) {
		t.Fatal("title change must change the hash")
	}
	summary.Title = "剧"
	if first != biz.CandidateHash(summary) {
		t.Fatal("same content must keep the hash")
	}
}
