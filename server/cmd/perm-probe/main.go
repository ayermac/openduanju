// Command perm-probe checks the role matrix over gRPC. The accounts and their
// passphrases come from the environment, never from the source, and nothing
// is printed but the classified outcome.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"time"

	v1 "openduanju-server/api/admin/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	grpcAddr = "127.0.0.1:19000"
	httpBase = "http://127.0.0.1:18081"
)

var reasonPattern = regexp.MustCompile(`reason=([A-Z_]+)`)

// session is the material a gRPC call needs.
type session struct {
	cookie string
	csrf   string
}

// login exchanges the passphrase for a session and its CSRF token.
func login(username, password string) (session, error) {
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	response, err := http.Post(httpBase+"/api/admin/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return session{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return session{}, fmt.Errorf("login %s: HTTP %d", username, response.StatusCode)
	}
	payload := struct {
		CSRFToken string `json:"csrf_token"`
	}{}
	_ = json.NewDecoder(response.Body).Decode(&payload)
	for _, cookie := range response.Cookies() {
		if cookie.Name == "dj_admin_session" {
			return session{cookie: cookie.Value, csrf: payload.CSRFToken}, nil
		}
	}
	return session{}, fmt.Errorf("no session cookie for %s", username)
}

func main() {
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fail("connect", err)
	}
	defer conn.Close()
	client := v1.NewAdminContentServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Anonymous access is refused.
	_, err = client.ListSeries(ctx, &v1.ListSeriesRequest{Page: 1, PageSize: 10})
	report("anonymous", "read", err)
	_, err = client.UpdateRevision(ctx, &v1.UpdateRevisionRequest{Id: 1, RevisionId: 1, ExpectedVersion: 1})
	report("anonymous", "edit", err)

	for _, account := range []struct {
		role     string
		username string
		password string
	}{
		{"admin", "admin", os.Getenv("PERM_PROBE_ADMIN_PASSWORD")},
		{"operator", "op-acc", os.Getenv("PERM_PROBE_OPERATOR_PASSWORD")},
		{"reviewer", "rev-acc", os.Getenv("PERM_PROBE_REVIEWER_PASSWORD")},
	} {
		if account.password == "" {
			fail("the passphrase of "+account.role+" is missing", fmt.Errorf("set PERM_PROBE_%s_PASSWORD", account.role))
		}
		auth, err := login(account.username, account.password)
		if err != nil {
			fail("login "+account.role, err)
		}
		callCtx := metadata.AppendToOutgoingContext(ctx,
			"x-admin-session", auth.cookie,
			"x-csrf-token", auth.csrf,
		)
		_, err = client.ListSeries(callCtx, &v1.ListSeriesRequest{Page: 1, PageSize: 10})
		report(account.role, "read", err)

		// The edit uses the version of the draft it just read.
		detail, err := client.GetSeries(callCtx, &v1.GetSeriesRequest{Id: 1})
		if err != nil {
			report(account.role, "read-draft", err)
			continue
		}
		if detail.Draft == nil {
			report(account.role, "edit", fmt.Errorf("the series has no working draft"))
			continue
		}
		_, err = client.UpdateRevision(callCtx, &v1.UpdateRevisionRequest{
			Id: detail.Id, RevisionId: detail.Draft.Id, ExpectedVersion: int32(detail.Draft.Version),
			Title: stringPtr("gRPC 探测"),
		})
		report(account.role, "edit", err)
	}
}

// report prints the classified outcome of one call.
func report(role, action string, err error) {
	if err == nil {
		fmt.Printf("%-10s %-6s -> ok\n", role, action)
		return
	}
	state, _ := status.FromError(err)
	reason := ""
	if match := reasonPattern.FindStringSubmatch(state.Message()); match != nil {
		reason = match[1]
	}
	fmt.Printf("%-10s %-6s -> %s %s\n", role, action, state.Code(), reason)
}

func stringPtr(value string) *string { return &value }

func fail(stage string, err error) {
	fmt.Printf("%s: failed: %v\n", stage, err)
	os.Exit(1)
}
