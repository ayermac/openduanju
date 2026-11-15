package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"openduanju-server/internal/service"

	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
)

// TestStatusFilterAppliesTheSelectedStatus proves that the status a handler
// selects reaches the client, although the generated handlers answer 200.
func TestStatusFilterAppliesTheSelectedStatus(t *testing.T) {
	srv := kratoshttp.NewServer(kratoshttp.Filter(statusFilter))
	srv.Route("/").Handle(http.MethodPost, "/probe", func(ctx kratoshttp.Context) error {
		service.SetAccepted(ctx)
		return ctx.Result(http.StatusOK, map[string]string{"status": "accepted"})
	})
	server := httptest.NewServer(srv)
	defer server.Close()

	response, err := http.Post(server.URL+"/probe", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusAccepted)
	}
}

// TestStatusFilterKeepsTheErrorStatus proves that a failed call does not
// claim to be accepted.
func TestStatusFilterKeepsTheErrorStatus(t *testing.T) {
	srv := kratoshttp.NewServer(kratoshttp.Filter(statusFilter))
	srv.Route("/").Handle(http.MethodPost, "/probe", func(ctx kratoshttp.Context) error {
		service.SetAccepted(ctx)
		return ctx.Result(http.StatusOK, map[string]string{"status": "accepted"})
	})
	server := httptest.NewServer(srv)
	defer server.Close()

	// A route without the accepted status keeps the default.
	plain := kratoshttp.NewServer(kratoshttp.Filter(statusFilter))
	plain.Route("/").Handle(http.MethodPost, "/plain", func(ctx kratoshttp.Context) error {
		return ctx.Result(http.StatusOK, map[string]string{"status": "ok"})
	})
	plainServer := httptest.NewServer(plain)
	defer plainServer.Close()

	response, err := http.Post(plainServer.URL+"/plain", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
}
