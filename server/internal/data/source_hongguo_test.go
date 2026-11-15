package data_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data"
)

func rankHTML(rankList string, page, total int) string {
	return fmt.Sprintf(`<!doctype html><html><body>
<script>window._ROUTER_DATA = {"loaderData":{"rank_layout":null,"rank_hot-drama/page":{"content":{"isSuccess":true,"rankList":[%s],"pagination":{"pageNum":%d,"totalPages":%d,"nextUrl":"https://example/next"}}}},"errors":null}</script>
</body></html>`, rankList, page, total)
}

func detailHTML(seriesID string, vidList string) string {
	return fmt.Sprintf(`<!doctype html><html><body>
<script>window._ROUTER_DATA = {"loaderData":{"detail_layout":null,"detail_page":{"isSuccess":true,"seriesDetail":{"series_id":"%s","series_name":"测试剧","series_cover":"https://p3.example/cover.jpg","series_intro":"  简介  ","tags":["玄幻"," 玄幻 ","穿越",""],"episode_cnt":3,"accessible_episode_cnt":1,"vid_list":[%s]}}},"errors":null}</script>
</body></html>`, seriesID, vidList)
}

func newSourceServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.RequestURI())
		mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}

func newHongguoSource(t *testing.T, origin string, maxBytes int64, transport http.RoundTripper) *data.HongguoSource {
	t.Helper()
	source, err := data.NewHongguoSource(data.HongguoSourceConfig{
		Origin:    origin,
		Timeout:   5 * time.Second,
		MaxBytes:  maxBytes,
		Transport: transport,
	})
	if err != nil {
		t.Fatalf("NewHongguoSource: %v", err)
	}
	return source
}

func TestHongguoSourceListsRanking(t *testing.T) {
	rankList := `{"seriesId":"111","title":"剧一","cover":"https://p3.example/a.jpg","rank":1,"tags":["漫剧","玄幻"],"description":"简介一","episodeCount":20,"openEpisodeCount":5,"heatText":"100万热度","scoreText":"9.5分"},
	{"seriesId":"222","title":"剧二","cover":"http://insecure.example/b.jpg","rank":2,"episodeCount":10},
	{"seriesId":"bad","title":"坏数据","rank":3},
	{"seriesId":"111","title":"重复","rank":4}`
	srv, paths := newSourceServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, rankHTML(rankList, 1, 5))
	})
	source := newHongguoSource(t, srv.URL, 1<<20, srv.Client().Transport)

	page, err := source.ListSeries(context.Background(), biz.ListSeriesRequest{Channel: "hot-drama", Page: 1})
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}
	if len(page.Items) != 2 || page.Dropped != 2 {
		t.Fatalf("items=%d dropped=%d", len(page.Items), page.Dropped)
	}
	if page.Items[0].ExternalID != "111" || page.Items[0].Title != "剧一" {
		t.Fatalf("first item = %+v", page.Items[0])
	}
	if page.Items[0].CoverURL != "https://p3.example/a.jpg" {
		t.Fatalf("cover = %q", page.Items[0].CoverURL)
	}
	if page.Items[1].CoverURL != "" {
		t.Fatalf("insecure cover must be dropped, got %q", page.Items[1].CoverURL)
	}
	if page.Items[0].Metrics["heat"] != "100万热度" {
		t.Fatalf("metrics = %v", page.Items[0].Metrics)
	}
	if page.TotalPages != 5 || page.Page != 1 {
		t.Fatalf("pagination = %d/%d", page.Page, page.TotalPages)
	}
	if len(*paths) != 1 || !strings.Contains((*paths)[0], "/rank/hot-drama?page=1") {
		t.Fatalf("paths = %v", *paths)
	}
}

func TestHongguoSourceRequestsSelectedPage(t *testing.T) {
	srv, paths := newSourceServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, rankHTML(`{"seriesId":"333","title":"第三页","rank":21}`, 2, 5))
	})
	source := newHongguoSource(t, srv.URL, 1<<20, srv.Client().Transport)
	page, err := source.ListSeries(context.Background(), biz.ListSeriesRequest{Channel: "hot-drama", Page: 2})
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}
	if page.Page != 2 || len(page.Items) != 1 {
		t.Fatalf("page = %+v", page)
	}
	if (*paths)[0] != "/rank/hot-drama?page=2" {
		t.Fatalf("path = %q", (*paths)[0])
	}
}

func TestHongguoSourceReadsSeriesDetail(t *testing.T) {
	srv, _ := newSourceServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, detailHTML("987654321", `"9001","9002","9003"`))
	})
	source := newHongguoSource(t, srv.URL, 1<<20, srv.Client().Transport)
	detail, err := source.GetSeries(context.Background(), "987654321")
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if detail.ExternalID != "987654321" || detail.Title != "测试剧" || detail.Description != "简介" {
		t.Fatalf("detail = %+v", detail)
	}
	if len(detail.Episodes) != 3 || detail.Episodes[0].Number != 1 || detail.Episodes[2].ExternalID != "9003" {
		t.Fatalf("episodes = %+v", detail.Episodes)
	}
	if len(detail.Tags) != 2 || detail.Tags[0] != "玄幻" {
		t.Fatalf("tags = %v", detail.Tags)
	}
}

func TestHongguoSourceRejectsDetailIdentityDrift(t *testing.T) {
	srv, _ := newSourceServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, detailHTML("111", `"9001"`))
	})
	source := newHongguoSource(t, srv.URL, 1<<20, srv.Client().Transport)
	_, err := source.GetSeries(context.Background(), "222")
	if code, _ := biz.SourceErrorCodeOf(err); code != biz.SourceCodeParseFailed {
		t.Fatalf("code = %s (%v), want %s", code, err, biz.SourceCodeParseFailed)
	}
}

func TestHongguoSourceRejectsDuplicateEpisodes(t *testing.T) {
	srv, _ := newSourceServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, detailHTML("111", `"9001","9001"`))
	})
	source := newHongguoSource(t, srv.URL, 1<<20, srv.Client().Transport)
	_, err := source.GetSeries(context.Background(), "111")
	if code, _ := biz.SourceErrorCodeOf(err); code != biz.SourceCodeParseFailed {
		t.Fatalf("code = %s (%v), want %s", code, err, biz.SourceCodeParseFailed)
	}
}

func TestHongguoSourceClassifiesStatus(t *testing.T) {
	cases := []struct {
		status    int
		code      biz.SourceErrorCode
		retryable bool
	}{
		{http.StatusForbidden, biz.SourceCodeRejected, false},
		{http.StatusNotFound, biz.SourceCodeNotFound, false},
		{http.StatusTooManyRequests, biz.SourceCodeRateLimited, true},
		{http.StatusBadGateway, biz.SourceCodeUnavailable, true},
		{http.StatusBadRequest, biz.SourceCodeRejected, false},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			srv, _ := newSourceServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			})
			source := newHongguoSource(t, srv.URL, 1<<20, srv.Client().Transport)
			_, err := source.ListSeries(context.Background(), biz.ListSeriesRequest{Channel: "hot-drama", Page: 1})
			code, _ := biz.SourceErrorCodeOf(err)
			if code != tc.code {
				t.Fatalf("code = %s (%v), want %s", code, err, tc.code)
			}
			if biz.SourceRetryable(err) != tc.retryable {
				t.Fatalf("retryable = %v, want %v", biz.SourceRetryable(err), tc.retryable)
			}
		})
	}
}

func TestHongguoSourceRejectsUnsafeRedirect(t *testing.T) {
	srv, _ := newSourceServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/elsewhere", http.StatusFound)
	})
	source := newHongguoSource(t, srv.URL, 1<<20, srv.Client().Transport)
	_, err := source.ListSeries(context.Background(), biz.ListSeriesRequest{Channel: "hot-drama", Page: 1})
	if code, _ := biz.SourceErrorCodeOf(err); code != biz.SourceCodeRejected {
		t.Fatalf("code = %s (%v), want %s", code, err, biz.SourceCodeRejected)
	}
}

func TestHongguoSourceRejectsMalformedPayload(t *testing.T) {
	srv, _ := newSourceServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html>no payload</html>`)
	})
	source := newHongguoSource(t, srv.URL, 1<<20, srv.Client().Transport)
	_, err := source.ListSeries(context.Background(), biz.ListSeriesRequest{Channel: "hot-drama", Page: 1})
	if code, _ := biz.SourceErrorCodeOf(err); code != biz.SourceCodeParseFailed {
		t.Fatalf("code = %s (%v), want %s", code, err, biz.SourceCodeParseFailed)
	}
}

func TestHongguoSourceEnforcesByteBudget(t *testing.T) {
	srv, _ := newSourceServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, strings.Repeat("x", 4096))
	})
	source := newHongguoSource(t, srv.URL, 1024, srv.Client().Transport)
	_, err := source.ListSeries(context.Background(), biz.ListSeriesRequest{Channel: "hot-drama", Page: 1})
	if code, _ := biz.SourceErrorCodeOf(err); code != biz.SourceCodeBudget {
		t.Fatalf("code = %s (%v), want %s", code, err, biz.SourceCodeBudget)
	}
}

func TestHongguoSourceRejectsInvalidConfig(t *testing.T) {
	cases := map[string]data.HongguoSourceConfig{
		"http origin":    {Origin: "http://hongguoduanju.com"},
		"credentials":    {Origin: "https://user:pass@hongguoduanju.com"},
		"with path":      {Origin: "https://hongguoduanju.com/rank"},
		"missing origin": {},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := data.NewHongguoSource(cfg)
			if code, _ := biz.SourceErrorCodeOf(err); code != biz.SourceCodeConfigInvalid {
				t.Fatalf("code = %s (%v), want %s", code, err, biz.SourceCodeConfigInvalid)
			}
		})
	}
}

func TestHongguoSourceRejectsUnsafeIdentifiers(t *testing.T) {
	source := newHongguoSource(t, "https://hongguoduanju.com", 1<<20, nil)
	if _, err := source.ListSeries(context.Background(), biz.ListSeriesRequest{Channel: "../admin", Page: 1}); err == nil {
		t.Fatal("unsafe channel must fail")
	}
	if _, err := source.GetSeries(context.Background(), "../../etc/passwd"); err == nil {
		t.Fatal("unsafe series id must fail")
	}
}

func TestHongguoSourceSkipsManifestMention(t *testing.T) {
	html := `<script>window._MODERNJS_ROUTE_MANIFEST = {"_ROUTER_DATA": "route-name"};</script>
<script>window._ROUTER_DATA = {"loaderData":{"rank_hot-drama/page":{"content":{"isSuccess":true,"rankList":[{"seriesId":"444","title":"清单之后"}],"pagination":{"pageNum":1,"totalPages":1}}}}}</script>`
	srv, _ := newSourceServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, html)
	})
	source := newHongguoSource(t, srv.URL, 1<<20, srv.Client().Transport)
	page, err := source.ListSeries(context.Background(), biz.ListSeriesRequest{Channel: "hot-drama", Page: 1})
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ExternalID != "444" {
		t.Fatalf("items = %+v", page.Items)
	}
}
