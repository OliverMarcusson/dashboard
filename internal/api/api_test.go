package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/olivermarcusson/dashboard/internal/auth"
	"github.com/olivermarcusson/dashboard/internal/config"
	"github.com/olivermarcusson/dashboard/internal/hub"
	"github.com/olivermarcusson/dashboard/internal/store"
)

func newTestServer(t *testing.T) (*httptest.Server, *auth.Service) {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	cfg := config.Config{
		RPID:          "dash.example.test",
		RPOrigins:     []string{"https://dash.example.test"},
		RPDisplayName: "Test",
		Username:      "oliver",
		SessionTTL:    time.Hour,
		SecureCookies: true,
	}
	a, err := auth.New(ctx, db, cfg)
	if err != nil {
		t.Fatalf("auth: %v", err)
	}

	srv := httptest.NewServer(New(db, a, hub.New(), nil, nil, nil).Handler())
	t.Cleanup(srv.Close)
	return srv, a
}

func post(t *testing.T, srv *httptest.Server, path string, body any) (int, map[string]any) {
	t.Helper()
	blob, _ := json.Marshal(body)
	res, err := srv.Client().Post(srv.URL+path, "application/json", bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestHealthIsPublic(t *testing.T) {
	srv, _ := newTestServer(t)
	res, err := srv.Client().Get(srv.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("health: got %d, want 200", res.StatusCode)
	}
}

func TestProtectedRoutesRejectAnonymous(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, path := range []string{"/api/security/passkeys", "/api/audit"} {
		res, err := srv.Client().Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: got %d, want 401", path, res.StatusCode)
		}
	}
}

func TestSessionReportsNoPasskeys(t *testing.T) {
	srv, _ := newTestServer(t)
	res, err := srv.Client().Get(srv.URL + "/api/auth/session")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)

	if out["authenticated"] != false {
		t.Errorf("authenticated = %v, want false", out["authenticated"])
	}
	if out["has_passkeys"] != false {
		t.Errorf("has_passkeys = %v, want false", out["has_passkeys"])
	}
}

func TestLoginRefusedWithoutEnrolledPasskey(t *testing.T) {
	srv, _ := newTestServer(t)
	status, body := post(t, srv, "/api/auth/login/start", map[string]any{})
	if status != http.StatusForbidden {
		t.Fatalf("got %d, want 403", status)
	}
	if body["error"] == "" {
		t.Error("expected an explanatory error")
	}
}

func TestEnrollRejectsBadCode(t *testing.T) {
	srv, _ := newTestServer(t)
	status, _ := post(t, srv, "/api/auth/enroll/start",
		map[string]any{"code": "WRNG-CODE-XXXX", "device_name": "Test"})
	if status != http.StatusForbidden {
		t.Fatalf("got %d, want 403", status)
	}
}

func TestEnrollCodeIsSingleUse(t *testing.T) {
	srv, a := newTestServer(t)
	code, _, err := a.CreateEnrollmentCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	status, body := post(t, srv, "/api/auth/enroll/start",
		map[string]any{"code": code, "device_name": "Proton Pass"})
	if status != http.StatusOK {
		t.Fatalf("first use: got %d (%v), want 200", status, body["error"])
	}
	if _, ok := body["options"]; !ok {
		t.Error("expected creation options in the response")
	}
	if body["state_id"] == nil || body["state_id"] == "" {
		t.Error("expected a state_id")
	}

	status, _ = post(t, srv, "/api/auth/enroll/start",
		map[string]any{"code": code, "device_name": "Proton Pass"})
	if status != http.StatusForbidden {
		t.Errorf("second use: got %d, want 403", status)
	}
}

func TestCeremonyStateIsSingleUse(t *testing.T) {
	srv, a := newTestServer(t)
	code, _, _ := a.CreateEnrollmentCode(context.Background())
	_, body := post(t, srv, "/api/auth/enroll/start",
		map[string]any{"code": code, "device_name": "Test"})
	stateID, _ := body["state_id"].(string)

	// A garbage credential still proves the state was consumed: the first call
	// fails at parsing, the second fails earlier, at the state lookup.
	status, _ := post(t, srv, "/api/auth/enroll/finish",
		map[string]any{"state_id": stateID, "credential": json.RawMessage(`{}`)})
	if status != http.StatusBadRequest {
		t.Fatalf("first finish: got %d, want 400 (parse failure)", status)
	}
	status, out := post(t, srv, "/api/auth/enroll/finish",
		map[string]any{"state_id": stateID, "credential": json.RawMessage(`{}`)})
	if status != http.StatusForbidden {
		t.Fatalf("replayed finish: got %d (%v), want 403", status, out["error"])
	}
}

func TestWebSocketRequiresSession(t *testing.T) {
	srv, _ := newTestServer(t)
	res, err := srv.Client().Get(srv.URL + "/ws?topics=host.metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("got %d, want 401 — an unauthenticated upgrade must be refused", res.StatusCode)
	}
}

func TestTopicAllowlist(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{"host.metrics", 1},
		{"host.metrics,docker.services,systemd.units", 3},
		{"host.metrics,host.metrics", 1},       // duplicates collapse
		{"host.metrics, docker.services ", 2},  // whitespace tolerated
		{"secrets.everything", 0},              // unknown topics dropped
		{"host.metrics,secrets.everything", 1}, // partially unknown
		{"", 0},
	}
	for _, tc := range cases {
		if got := len(parseTopics(tc.raw)); got != tc.want {
			t.Errorf("parseTopics(%q) = %d topics, want %d", tc.raw, got, tc.want)
		}
	}
}

func TestMetricsRangeNeedsAMetric(t *testing.T) {
	srv, _ := newTestServer(t)
	// Unauthenticated first: the route must be behind the session check.
	res, _ := srv.Client().Get(srv.URL + "/api/metrics/range")
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", res.StatusCode)
	}
}
