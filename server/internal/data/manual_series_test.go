package data_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data"
	"openduanju-server/internal/data/ent/contentrevision"
	"openduanju-server/internal/data/ent/contentseries"
	"openduanju-server/internal/data/ent/revisionepisode"
)

// manualInput is the form an operator submits for a work without a provider.
func manualInput(title string, numbers ...int) biz.ManualSeriesInput {
	episodes := make([]biz.ManualEpisodeInput, 0, len(numbers))
	for _, number := range numbers {
		episodes = append(episodes, biz.ManualEpisodeInput{Number: number})
	}
	return biz.ManualSeriesInput{Title: title, Description: "人工创建", Episodes: episodes}
}

// TestManualSeriesCreatesADraftWithEpisodes proves the created work is a
// normal draft that carries no provider identity, and that recording it
// downloads, processes and reviews nothing.
func TestManualSeriesCreatesADraftWithEpisodes(t *testing.T) {
	client := openMediaClient(t)
	ctx := context.Background()
	catalog := data.NewSourceCatalogRepoWithDB(client)
	uc, _ := biz.NewSourceCatalog(catalog, &fakeWeb{}, nil, nil)

	view, err := uc.CreateManualSeries(ctx, manualInput("人工短剧", 1, 2, 5))
	if err != nil {
		t.Fatalf("create manual series: %v", err)
	}
	if view.Series == nil || view.Revision == nil {
		t.Fatalf("created view: %+v", view)
	}
	series, err := client.ContentSeries.Get(ctx, view.Series.ID)
	if err != nil {
		t.Fatalf("read series: %v", err)
	}
	if series.ExternalID != nil {
		t.Fatalf("a manual work carries an external id: %v", *series.ExternalID)
	}
	if series.SourceCode != "" || series.CandidateID != nil {
		t.Fatalf("a manual work names a source: %q %v", series.SourceCode, series.CandidateID)
	}
	if series.Title != "人工短剧" || series.Status != "draft" {
		t.Fatalf("series: %+v", series)
	}
	if series.CurrentDraftRevisionID == nil || *series.CurrentDraftRevisionID != view.Revision.ID {
		t.Fatalf("the series does not point at its draft")
	}
	revision, err := client.ContentRevision.Get(ctx, view.Revision.ID)
	if err != nil {
		t.Fatalf("read revision: %v", err)
	}
	if revision.State != biz.RevisionStateDraft || revision.Version != 1 || revision.Title != "人工短剧" {
		t.Fatalf("revision: %+v", revision)
	}
	episodes, err := client.RevisionEpisode.Query().
		Where(revisionepisode.RevisionIDEQ(revision.ID)).
		Order(revisionepisode.ByEpisodeNo()).
		All(ctx)
	if err != nil {
		t.Fatalf("read episodes: %v", err)
	}
	if len(episodes) != 3 {
		t.Fatalf("episodes: %d", len(episodes))
	}
	for index, number := range []int{1, 2, 5} {
		episode := episodes[index]
		if int(episode.EpisodeNo) != number || !episode.Selected {
			t.Fatalf("episode %d: no=%d selected=%v", index, episode.EpisodeNo, episode.Selected)
		}
		if episode.ExternalID != nil {
			t.Fatalf("a manual episode carries an external id: %v", *episode.ExternalID)
		}
	}
	// Recording a draft must not download, transcode, upload or open a review.
	for name, count := range map[string]int{
		"media_batch":    countRows(t, client.MediaBatch.Query().Count),
		"media_item":     countRows(t, client.MediaItem.Query().Count),
		"upload_session": countRows(t, client.UploadSession.Query().Count),
		"review_task":    countRows(t, client.ReviewTask.Query().Count),
		"publication":    countRows(t, client.Publication.Query().Count),
	} {
		if count != 0 {
			t.Fatalf("creating a draft wrote %d %s rows", count, name)
		}
	}
}

func countRows(t *testing.T, count func(context.Context) (int, error)) int {
	t.Helper()
	value, err := count(context.Background())
	if err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return value
}

// TestManualSeriesRejectsInvalidInputWithoutWriting checks every rule of the
// form: a refused submission leaves no series, revision or episode behind.
func TestManualSeriesRejectsInvalidInputWithoutWriting(t *testing.T) {
	client := openMediaClient(t)
	ctx := context.Background()
	catalog := data.NewSourceCatalogRepoWithDB(client)
	uc, _ := biz.NewSourceCatalog(catalog, &fakeWeb{}, nil, nil)

	cases := []struct {
		name  string
		input biz.ManualSeriesInput
		want  error
	}{
		{"no title", manualInput("   ", 1), biz.ErrManualTitleRequired},
		{"long title", manualInput(strings.Repeat("长", biz.ManualTitleLimit+1), 1), biz.ErrManualTitleRequired},
		{"no episode", manualInput("只有标题"), biz.ErrManualEpisodes},
		{"episode number zero", manualInput("错了", 0), biz.ErrManualEpisodeNo},
		{"episode number above the limit", manualInput("错了", biz.ManualEpisodeNoMax+1), biz.ErrManualEpisodeNo},
		{"duplicate number", manualInput("重复集", 1, 2, 2), biz.ErrDuplicateEpisodeNo},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := uc.CreateManualSeries(ctx, testCase.input); !errors.Is(err, testCase.want) {
				t.Fatalf("create: %v, want %v", err, testCase.want)
			}
			if series := countRows(t, client.ContentSeries.Query().Count); series != 0 {
				t.Fatalf("a refused form wrote %d series", series)
			}
			if revisions := countRows(t, client.ContentRevision.Query().Count); revisions != 0 {
				t.Fatalf("a refused form wrote %d revisions", revisions)
			}
			if episodes := countRows(t, client.RevisionEpisode.Query().Count); episodes != 0 {
				t.Fatalf("a refused form wrote %d episodes", episodes)
			}
		})
	}
}

// TestManualSeriesIsIdempotentPerRequestID proves a double submission returns
// the work that already exists, and that the key cannot be reused for another
// payload.
func TestManualSeriesIsIdempotentPerRequestID(t *testing.T) {
	client := openMediaClient(t)
	ctx := context.Background()
	catalog := data.NewSourceCatalogRepoWithDB(client)
	uc, _ := biz.NewSourceCatalog(catalog, &fakeWeb{}, nil, nil)

	input := manualInput("幂等剧", 1, 2)
	input.RequestID = "request-1"
	first, err := uc.CreateManualSeries(ctx, input)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	// The same form again: same work, no second row.
	again, err := uc.CreateManualSeries(ctx, input)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if again.Series.ID != first.Series.ID {
		t.Fatalf("the replay created series %d after %d", again.Series.ID, first.Series.ID)
	}
	if series := countRows(t, client.ContentSeries.Query().Count); series != 1 {
		t.Fatalf("replay left %d series", series)
	}
	// The same key with another payload is refused instead of returning the
	// wrong work.
	other := manualInput("另一个作品", 1, 2)
	other.RequestID = "request-1"
	if _, err := uc.CreateManualSeries(ctx, other); !errors.Is(err, biz.ErrIdempotencyConflict) {
		t.Fatalf("reused key: %v", err)
	}
	if series := countRows(t, client.ContentSeries.Query().Count); series != 1 {
		t.Fatalf("a conflicting key wrote %d series", series)
	}
	if _, err := client.ContentRevision.Query().
		Where(contentrevision.SeriesIDNEQ(first.Series.ID)).
		Count(ctx); err != nil {
		t.Fatalf("count other revisions: %v", err)
	}
}

// TestManualSeriesConcurrentSubmissionCreatesOneWork proves two clicks that
// race still produce one work.
func TestManualSeriesConcurrentSubmissionCreatesOneWork(t *testing.T) {
	client := openMediaClient(t)
	ctx := context.Background()
	catalog := data.NewSourceCatalogRepoWithDB(client)
	uc, _ := biz.NewSourceCatalog(catalog, &fakeWeb{}, nil, nil)

	input := manualInput("并发剧", 1, 2)
	input.RequestID = "request-race"
	var wait sync.WaitGroup
	ids := make([]int64, 4)
	errs := make([]error, 4)
	for index := range ids {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			view, err := uc.CreateManualSeries(ctx, input)
			if err != nil {
				errs[index] = err
				return
			}
			ids[index] = view.Series.ID
		}(index)
	}
	wait.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("concurrent create %d: %v", index, err)
		}
		if ids[index] != ids[0] {
			t.Fatalf("concurrent creates answered %v", ids)
		}
	}
	if series := countRows(t, client.ContentSeries.Query().Count); series != 1 {
		t.Fatalf("concurrent submission created %d series", series)
	}
}

// TestManualSeriesEntersTheExistingSubmissionPath proves the created work is
// submitted exactly like an imported one: the freeze refuses it while no
// episode has a confirmed input.
func TestManualSeriesEntersTheExistingSubmissionPath(t *testing.T) {
	client := openMediaClient(t)
	ctx := context.Background()
	catalog := data.NewSourceCatalogRepoWithDB(client)
	uc, _ := biz.NewSourceCatalog(catalog, &fakeWeb{}, nil, nil)
	view, err := uc.CreateManualSeries(ctx, manualInput("提交剧", 1, 2))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// The work shows up in the content list with its draft.
	page, total, err := catalog.ListSeries(ctx, biz.ContentSeriesQuery{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("list series: %v", err)
	}
	if total != 1 || len(page) != 1 || page[0].RevisionState != biz.RevisionStateDraft {
		t.Fatalf("content list: %+v total=%d", page, total)
	}
	grant := seedGrant(t, client, view.Series.ID)
	_, err = data.NewMediaBatchRepoWithDB(client).FreezeAndCreateBatch(ctx, biz.FreezeBatchRequest{
		RevisionID:                   view.Revision.ID,
		ExpectedVersion:              view.Revision.Version,
		ProfileVersion:               "web-mp4-h264-aac-v1",
		InputKind:                    "manual",
		Actor:                        "operator",
		AuthorizationGrantID:         grant.ID,
		ExpectedAuthorizationVersion: 1,
	})
	if !errors.Is(err, biz.ErrBatchInputNotReady) {
		t.Fatalf("freeze without confirmed inputs: %v", err)
	}
	// The refused submission kept the draft editable and wrote no batch.
	stored, err := client.ContentSeries.Get(ctx, view.Series.ID)
	if err != nil {
		t.Fatalf("read series: %v", err)
	}
	if stored.Status != "draft" {
		t.Fatalf("series status after a refused freeze: %s", stored.Status)
	}
	if batches := countRows(t, client.MediaBatch.Query().Count); batches != 0 {
		t.Fatalf("the refused freeze wrote %d batches", batches)
	}
	if _, err := client.ContentSeries.Query().
		Where(contentseries.IDEQ(view.Series.ID)).
		Only(ctx); err != nil {
		t.Fatalf("series vanished: %v", err)
	}
}
