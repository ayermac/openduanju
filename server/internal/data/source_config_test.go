package data_test

import (
	"context"
	"errors"
	"testing"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data"
	"openduanju-server/internal/data/ent"
	"openduanju-server/internal/data/ent/source"
	"openduanju-server/internal/data/ent/sourcechannel"
)

// restoreChannel puts the seeded channel back into its deployed state, so one
// test cannot leave a pause - or an adapter pointed at a fake - behind for the
// next one. The adapter key is part of that state: a test that points the
// channel at its own fake must not break the deployed configuration.
func restoreChannel(t *testing.T, client *ent.Client) {
	t.Helper()
	ctx := context.Background()
	_, err := client.SourceChannel.Update().
		Where(sourcechannel.CodeEQ("hot-drama")).
		SetName("热播榜").
		SetAdapterKey("hongguo_web").
		SetStatus(biz.SourceStatusEnabled).
		SetRatePerMinute(30).
		SetTimeoutSeconds(20).
		SetVersion(1).
		Save(ctx)
	if err != nil {
		t.Fatalf("restore channel: %v", err)
	}
	_, err = client.Source.Update().Where(source.CodeEQ("hongguo")).
		SetStatus(biz.SourceStatusEnabled).SetVersion(1).Save(ctx)
	if err != nil {
		t.Fatalf("restore source: %v", err)
	}
}

// hongguoAdapter is the deployed metadata adapter. Its declaration is offline,
// so a configuration test never calls the source.
func hongguoAdapter(t *testing.T) *data.HongguoSource {
	t.Helper()
	adapter, err := data.NewHongguoSource(data.HongguoSourceConfig{Origin: "https://hongguoduanju.com"})
	if err != nil {
		t.Fatalf("build adapter: %v", err)
	}
	return adapter
}

// TestSourceChannelConfigurationIsVersionChecked proves the configuration is
// editable, that a stale edit is refused, and that pausing is a separate,
// explicit action.
func TestSourceChannelConfigurationIsVersionChecked(t *testing.T) {
	client := openMediaClient(t)
	restoreChannel(t, client)
	defer restoreChannel(t, client)
	ctx := context.Background()
	catalog := data.NewSourceCatalogRepoWithDB(client)
	uc, _ := biz.NewSourceCatalog(catalog, hongguoAdapter(t), nil, nil)

	sources, err := uc.ListSources(ctx)
	if err != nil || len(sources) == 0 {
		t.Fatalf("list sources: %v %d", err, len(sources))
	}
	channels, err := uc.ListChannels(ctx, 0)
	if err != nil || len(channels) == 0 {
		t.Fatalf("list channels: %v %d", err, len(channels))
	}
	channel := channels[0]
	if channel.SourceCode != "hongguo" || channel.Code != "hot-drama" {
		t.Fatalf("channel: %+v", channel)
	}
	if channel.RatePerMinute != 30 || channel.TimeoutSecond != 20 {
		t.Fatalf("the seeded configuration is not the deployed one: %+v", channel)
	}

	updated, err := uc.UpdateChannel(ctx, channel.ID, channel.Version, biz.ChannelUpdate{
		Name:          "热播榜（限频已调）",
		EntryKind:     channel.EntryKind,
		EntryCode:     channel.EntryCode,
		BaseURL:       channel.BaseURL,
		AllowedHosts:  channel.AllowedHosts,
		RatePerMinute: 45,
		TimeoutSecond: 25,
	})
	if err != nil {
		t.Fatalf("update channel: %v", err)
	}
	if updated.RatePerMinute != 45 || updated.TimeoutSecond != 25 || updated.Version != channel.Version+1 {
		t.Fatalf("updated channel: %+v", updated)
	}
	// The stale version is refused and nothing is written.
	if _, err := uc.UpdateChannel(ctx, channel.ID, channel.Version, biz.ChannelUpdate{
		Name:          "过期修改",
		EntryKind:     channel.EntryKind,
		EntryCode:     channel.EntryCode,
		BaseURL:       channel.BaseURL,
		AllowedHosts:  channel.AllowedHosts,
		RatePerMinute: 60,
		TimeoutSecond: 25,
	}); !errors.Is(err, biz.ErrChannelVersion) {
		t.Fatalf("stale update: %v", err)
	}
	current, err := uc.GetChannel(ctx, channel.ID)
	if err != nil {
		t.Fatalf("read channel: %v", err)
	}
	if current.RatePerMinute != 45 || current.Name == "过期修改" {
		t.Fatalf("the refused edit changed the row: %+v", current)
	}

	paused, err := uc.SetChannelStatus(ctx, channel.ID, current.Version, biz.SourceStatusPaused)
	if err != nil {
		t.Fatalf("pause channel: %v", err)
	}
	if paused.Status != biz.SourceStatusPaused || !paused.Paused || paused.PausedCause != "channel" {
		t.Fatalf("paused channel: %+v", paused)
	}
	if _, err := uc.SetChannelStatus(ctx, channel.ID, current.Version, biz.SourceStatusEnabled); !errors.Is(err, biz.ErrChannelVersion) {
		t.Fatalf("stale pause: %v", err)
	}
}

// TestSourceChannelRejectsUnsupportedConfiguration proves the server refuses
// everything the adapter did not declare, without touching the stored row.
func TestSourceChannelRejectsUnsupportedConfiguration(t *testing.T) {
	client := openMediaClient(t)
	restoreChannel(t, client)
	ctx := context.Background()
	catalog := data.NewSourceCatalogRepoWithDB(client)
	uc, _ := biz.NewSourceCatalog(catalog, hongguoAdapter(t), nil, nil)
	channels, err := uc.ListChannels(ctx, 0)
	if err != nil || len(channels) == 0 {
		t.Fatalf("list channels: %v", err)
	}
	channel := channels[0]
	base := biz.ChannelUpdate{
		Name:          "热播榜",
		EntryKind:     channel.EntryKind,
		EntryCode:     channel.EntryCode,
		BaseURL:       channel.BaseURL,
		AllowedHosts:  channel.AllowedHosts,
		RatePerMinute: 30,
		TimeoutSecond: 20,
	}
	cases := []struct {
		name   string
		update func(biz.ChannelUpdate) biz.ChannelUpdate
		want   error
	}{
		{"unknown entry", func(u biz.ChannelUpdate) biz.ChannelUpdate {
			u.EntryCode = "category"
			return u
		}, biz.ErrChannelEntry},
		{"plain http origin", func(u biz.ChannelUpdate) biz.ChannelUpdate {
			u.BaseURL = "http://hongguoduanju.com"
			return u
		}, biz.ErrChannelURL},
		{"origin with a path", func(u biz.ChannelUpdate) biz.ChannelUpdate {
			u.BaseURL = "https://hongguoduanju.com/rank"
			return u
		}, biz.ErrChannelURL},
		{"host outside the declaration", func(u biz.ChannelUpdate) biz.ChannelUpdate {
			u.BaseURL = "https://example.com"
			u.AllowedHosts = "example.com"
			return u
		}, biz.ErrChannelURL},
		{"host missing from the allowlist", func(u biz.ChannelUpdate) biz.ChannelUpdate {
			u.AllowedHosts = "www.hongguoduanju.com"
			return u
		}, biz.ErrChannelURL},
		{"rate out of range", func(u biz.ChannelUpdate) biz.ChannelUpdate {
			u.RatePerMinute = 0
			return u
		}, biz.ErrChannelRate},
		{"timeout out of range", func(u biz.ChannelUpdate) biz.ChannelUpdate {
			u.TimeoutSecond = 1
			return u
		}, biz.ErrChannelTimeout},
		{"empty name", func(u biz.ChannelUpdate) biz.ChannelUpdate {
			u.Name = "  "
			return u
		}, biz.ErrChannelName},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := uc.UpdateChannel(ctx, channel.ID, channel.Version, testCase.update(base)); !errors.Is(err, testCase.want) {
				t.Fatalf("update: %v, want %v", err, testCase.want)
			}
			current, err := uc.GetChannel(ctx, channel.ID)
			if err != nil {
				t.Fatalf("read channel: %v", err)
			}
			if current.Version != channel.Version || current.RatePerMinute != 30 {
				t.Fatalf("the refused edit changed the row: %+v", current)
			}
		})
	}
}

// TestPausedChannelRefusesNewRunsAndKeepsHistory proves a pause stops new
// collection without touching the runs, the candidates or the published work.
func TestPausedChannelRefusesNewRunsAndKeepsHistory(t *testing.T) {
	client := openMediaClient(t)
	restoreChannel(t, client)
	// The pause must not leak into the next test.
	defer restoreChannel(t, client)
	ctx := context.Background()
	catalog := data.NewSourceCatalogRepoWithDB(client)
	uc, _ := biz.NewSourceCatalog(catalog, hongguoAdapter(t), nil, nil)

	plan, err := uc.CreatePlan(ctx, biz.IngestionPlan{
		Code: "paused-plan", Name: "暂停用例", SourceCode: "hongguo", Channel: "hot-drama",
		PageStart: 1, PageEnd: 1, RequestBudget: 5, ItemBudget: 5, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	run, err := catalog.CreateRun(ctx, plan, biz.RunKindRanking, 0)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	channel, err := uc.ListChannels(ctx, 0)
	if err != nil || len(channel) == 0 {
		t.Fatalf("list channels: %v", err)
	}
	if _, err := uc.SetChannelStatus(ctx, channel[0].ID, channel[0].Version, biz.SourceStatusPaused); err != nil {
		t.Fatalf("pause: %v", err)
	}
	// A new run is refused while the channel is paused. The plan of the queued
	// run is busy, so the check uses a second plan.
	second, err := uc.CreatePlan(ctx, biz.IngestionPlan{
		Code: "paused-plan-2", Name: "暂停用例二", SourceCode: "hongguo", Channel: "hot-drama",
		PageStart: 1, PageEnd: 1, RequestBudget: 5, ItemBudget: 5, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create second plan: %v", err)
	}
	if _, err := catalog.CreateRun(ctx, second, biz.RunKindRanking, 0); !errors.Is(err, biz.ErrSourcePaused) {
		t.Fatalf("run on a paused channel: %v", err)
	}
	runs := countRows(t, client.IngestionRun.Query().Count)
	if runs != 1 {
		t.Fatalf("the refused run wrote %d runs", runs)
	}
	// The run that was already queued keeps its own snapshot and its place.
	stored, err := client.IngestionRun.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("read run: %v", err)
	}
	if stored.Status != biz.RunStatusQueued {
		t.Fatalf("the queued run changed: %s", stored.Status)
	}
	if stored.PlanSnapshot["channel_base_url"] != channel[0].BaseURL {
		t.Fatalf("the run snapshot lost the channel: %+v", stored.PlanSnapshot)
	}
}

// TestPlanRequiresAConfiguredChannel proves a plan cannot name a channel the
// server does not know, and that the rate is taken from the channel when the
// plan does not override it.
func TestPlanRequiresAConfiguredChannel(t *testing.T) {
	client := openMediaClient(t)
	restoreChannel(t, client)
	ctx := context.Background()
	catalog := data.NewSourceCatalogRepoWithDB(client)
	uc, _ := biz.NewSourceCatalog(catalog, hongguoAdapter(t), nil, nil)

	if _, err := uc.CreatePlan(ctx, biz.IngestionPlan{
		Code: "unknown-channel", Name: "未知入口", SourceCode: "hongguo", Channel: "not-configured",
		PageStart: 1, PageEnd: 1, RequestBudget: 5, ItemBudget: 5,
	}); err == nil {
		t.Fatal("a plan named a channel that does not exist")
	}
	plan, err := uc.CreatePlan(ctx, biz.IngestionPlan{
		Code: "inherit-plan", Name: "继承限频", SourceCode: "hongguo", Channel: "hot-drama",
		PageStart: 1, PageEnd: 1, RequestBudget: 5, ItemBudget: 5,
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	run, err := catalog.CreateRun(ctx, plan, biz.RunKindRanking, 0)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if run.RatePerMinute != 30 {
		t.Fatalf("the run did not inherit the channel rate: %d", run.RatePerMinute)
	}
	stored, err := client.IngestionRun.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("read run: %v", err)
	}
	if stored.PlanSnapshot["rate_source"] != "channel" {
		t.Fatalf("the snapshot does not say where the rate came from: %+v", stored.PlanSnapshot)
	}
	// An explicit plan rate wins over the channel value.
	override := *plan
	override.Code = "override-plan"
	override.RatePerMinute = 45
	overridePlan, err := uc.CreatePlan(ctx, override)
	if err != nil {
		t.Fatalf("create plan with a rate: %v", err)
	}
	overrideRun, err := catalog.CreateRun(ctx, overridePlan, biz.RunKindRanking, 0)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if overrideRun.RatePerMinute != 45 {
		t.Fatalf("the plan rate was not used: %d", overrideRun.RatePerMinute)
	}
	overrideStored, err := client.IngestionRun.Get(ctx, overrideRun.ID)
	if err != nil {
		t.Fatalf("read run: %v", err)
	}
	if overrideStored.PlanSnapshot["rate_source"] != "plan" {
		t.Fatalf("the snapshot does not say where the rate came from: %+v", overrideStored.PlanSnapshot)
	}
}

// TestChannelProbeIsExplicitAndBounded proves the probe calls the source once,
// under the channel timeout, and that a paused channel cannot be probed.
func TestChannelProbeIsExplicitAndBounded(t *testing.T) {
	client := openMediaClient(t)
	restoreChannel(t, client)
	defer restoreChannel(t, client)
	ctx := context.Background()
	catalog := data.NewSourceCatalogRepoWithDB(client)
	adapter := &fakeWeb{page: &biz.SeriesPage{Channel: "hot-drama", TotalPages: 3, Items: []biz.SeriesSummary{{ExternalID: "1", Title: "一"}}}}
	uc, _ := biz.NewSourceCatalog(catalog, adapter, nil, nil)
	channels, err := uc.ListChannels(ctx, 0)
	if err != nil || len(channels) == 0 {
		t.Fatalf("list channels: %v", err)
	}
	channel := channels[0]
	// The probe runs against the fake adapter, so the test never calls out.
	if _, err := client.SourceChannel.UpdateOneID(channel.ID).SetAdapterKey("fake_web").Save(ctx); err != nil {
		t.Fatalf("point the channel at the fake adapter: %v", err)
	}
	refreshed, err := uc.GetChannel(ctx, channel.ID)
	if err != nil {
		t.Fatalf("read channel: %v", err)
	}
	channel = *refreshed

	probe, err := uc.TestChannel(ctx, channel.ID)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !probe.OK || probe.Items != 1 || probe.TotalPages != 3 {
		t.Fatalf("probe: %+v", probe)
	}
	if adapter.calls != 1 {
		t.Fatalf("the probe called the source %d times", adapter.calls)
	}
	// A paused channel is not probed at all.
	if _, err := uc.SetChannelStatus(ctx, channel.ID, channel.Version, biz.SourceStatusPaused); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if _, err := uc.TestChannel(ctx, channel.ID); !errors.Is(err, biz.ErrChannelProbeUsage) {
		t.Fatalf("probe on a paused channel: %v", err)
	}
	if adapter.calls != 1 {
		t.Fatalf("the refused probe called the source %d times", adapter.calls)
	}
}

// TestSourceAndChannelRowsStayWhenThePlanIsGone proves the configuration is
// not owned by a plan: removing the plan keeps the channel.
func TestSourceAndChannelRowsStayWhenThePlanIsGone(t *testing.T) {
	client := openMediaClient(t)
	restoreChannel(t, client)
	ctx := context.Background()
	catalog := data.NewSourceCatalogRepoWithDB(client)
	uc, _ := biz.NewSourceCatalog(catalog, hongguoAdapter(t), nil, nil)
	if _, err := uc.CreatePlan(ctx, biz.IngestionPlan{
		Code: "keep-plan", Name: "保留配置", SourceCode: "hongguo", Channel: "hot-drama",
		PageStart: 1, PageEnd: 1, RequestBudget: 5, ItemBudget: 5,
	}); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if _, err := client.IngestionPlan.Delete().Exec(ctx); err != nil {
		t.Fatalf("delete plan: %v", err)
	}
	if channels := countRows(t, client.SourceChannel.Query().Count); channels == 0 {
		t.Fatal("the channel was removed with its plan")
	}
	if runs := countRows(t, client.IngestionRun.Query().Count); runs != 0 {
		t.Fatalf("unexpected runs: %d", runs)
	}
}
