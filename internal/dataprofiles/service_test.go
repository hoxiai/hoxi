package dataprofiles

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestProfileCredentialIsolationAndReadOnlyGuards(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "dataprofiles.json")

	const secretBearer = "tok_live_ultra_confidential_998877"
	var gotAuthHeader, gotCustomHeader, gotPath, gotMethod string
	mockClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			gotAuthHeader = r.Header.Get("Authorization")
			gotCustomHeader = r.Header.Get("X-Node-Scope")
			gotPath = r.URL.RequestURI()
			gotMethod = r.Method
			body, _ := json.Marshal(map[string]any{
				"healthy": true,
				"echo":    secretBearer, // verify response secret redaction too!
			})
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(bytes.NewReader(body)),
			}, nil
		}),
	}

	svc := NewService(storePath, mockClient)
	ctx := context.Background()

	pub, err := svc.Upsert(Profile{
		ID:          "gopanel-prod",
		Name:        "GoPanel Production",
		Kind:        KindHTTPAPI,
		BaseURL:     "https://panel.example.internal",
		BearerToken: secretBearer,
		Headers:     map[string]string{"X-Node-Scope": "cluster-a"},
		ReadOnly:    true,
	})
	if err != nil {
		t.Fatalf("Upsert http_api failed: %v", err)
	}
	if !pub.HasAuth || pub.TargetHint != "https://panel.example.internal" {
		t.Fatalf("unexpected public profile metadata: %+v", pub)
	}

	// Verify list output leaks neither BearerToken nor custom header values.
	listRes, err := svc.Invoke(ctx, InvokeRequest{Action: "list"})
	if err != nil {
		t.Fatalf("Invoke list failed: %v", err)
	}
	rawList, _ := json.Marshal(listRes)
	if strings.Contains(string(rawList), secretBearer) || strings.Contains(string(rawList), "cluster-a") {
		t.Fatalf("secret credential leaked in list output: %s", string(rawList))
	}

	// Verify SSRF / host-override / path-traversal guard rejects external URLs, protocol-relative paths, and '..' segments.
	for _, badEP := range []string{
		"https://attacker.invalid/steal",
		"//attacker.invalid/steal",
		"api/v1/nodes",
		"/../../v1/other-tenant",
		"/api/%2e%2e/admin",
		`/api\..\..\secret`,
	} {
		if _, err := svc.Invoke(ctx, InvokeRequest{
			Action:   "http_request",
			Profile:  "gopanel-prod",
			Method:   "GET",
			Endpoint: badEP,
		}); err == nil {
			t.Fatalf("expected endpoint %q to be rejected", badEP)
		}
	}

	// Verify leading '-' flag-injection inputs are rejected on SQLite, Postgres/MySQL, and SSH profiles.
	for _, badProf := range []Profile{
		{ID: "bad-sqlite", Kind: KindSQLite, DBPath: "-cmd"},
		{ID: "bad-pg-user", Kind: KindPostgres, Host: "127.0.0.1", Username: "-o"},
		{ID: "bad-pg-db", Kind: KindPostgres, Host: "127.0.0.1", Database: "-d"},
		{ID: "bad-ssh-key", Kind: KindSSH, Host: "example.com", KeyPath: "-oProxyCommand=sh"},
	} {
		if _, err := svc.Upsert(badProf); err == nil {
			t.Fatalf("expected leading-dash field in %+v to be rejected", badProf)
		}
	}

	// Verify ReadOnly blocks mutating HTTP verbs.
	if _, err := svc.Invoke(ctx, InvokeRequest{
		Action:   "http_request",
		Profile:  "gopanel-prod",
		Method:   "POST",
		Endpoint: "/api/v1/deploy",
	}); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("expected read-only HTTP method guard, got: %v", err)
	}

	// Verify GET injects credentials and redacts any accidentally echoed secret in response.
	httpOut, err := svc.Invoke(ctx, InvokeRequest{
		Action:   "http_request",
		Profile:  "gopanel-prod",
		Method:   "GET",
		Endpoint: "/api/v1/nodes?region=ap",
	})
	if err != nil {
		t.Fatalf("Invoke http_request failed: %v", err)
	}
	if gotAuthHeader != "Bearer "+secretBearer || gotCustomHeader != "cluster-a" || gotPath != "/api/v1/nodes?region=ap" || gotMethod != "GET" {
		t.Fatalf("unexpected injected headers/path: auth=%q custom=%q path=%q method=%q", gotAuthHeader, gotCustomHeader, gotPath, gotMethod)
	}
	rawOut, _ := json.Marshal(httpOut)
	if strings.Contains(string(rawOut), secretBearer) || !strings.Contains(string(rawOut), "[REDACTED]") {
		t.Fatalf("expected echoed secret to be redacted in tool output, got: %s", string(rawOut))
	}

	// Verify desktop-only HTTP routes reject remote callers with 403.
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux, func(r *http.Request) bool {
		return r.Header.Get("X-Test-Remote") == "1"
	})
	remReq := httptest.NewRequest(http.MethodGet, "/api/hoxi/dataprofiles", nil)
	remReq.Header.Set("X-Test-Remote", "1")
	remW := httptest.NewRecorder()
	mux.ServeHTTP(remW, remReq)
	if remW.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for remote caller, got %d", remW.Code)
	}
}

func TestCorruptDataProfilesStoreBackedUpBeforeFail(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "dataprofiles.json")
	corruptPayload := []byte(`{"profiles": [BROKEN_JSON`)
	if err := os.WriteFile(storePath, corruptPayload, 0o600); err != nil {
		t.Fatal(err)
	}

	svc := NewService(storePath, nil)
	if _, err := svc.List(); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("expected corrupt store error, got %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var backupPath string
	for _, ent := range entries {
		if strings.HasPrefix(ent.Name(), "dataprofiles.json.corrupt-") {
			backupPath = filepath.Join(dir, ent.Name())
			break
		}
	}
	if backupPath == "" {
		t.Fatal("expected corrupt dataprofiles.json to be moved aside to .corrupt-* backup")
	}
	gotBackup, err := os.ReadFile(backupPath)
	if err != nil || string(gotBackup) != string(corruptPayload) {
		t.Fatalf("backup content mismatch: err=%v got=%q", err, string(gotBackup))
	}

	// Subsequent Upsert succeeds cleanly without touching the backup file.
	if _, err := svc.Upsert(Profile{ID: "clean-1", BaseURL: "https://api.example.com"}); err != nil {
		t.Fatalf("Upsert after backup failed: %v", err)
	}
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("backup file should remain intact: %v", err)
	}
}

func TestDefaultHTTPClientBlocksCrossOriginRedirectCredentialLeak(t *testing.T) {
	var leakedHeader string
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leakedHeader = r.Header.Get("X-Api-Secret")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer evil.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+"/collect", http.StatusFound)
	}))
	defer origin.Close()

	svc := NewService(filepath.Join(t.TempDir(), "dataprofiles.json"), nil)
	if _, err := svc.Upsert(Profile{
		ID:          "redir-test",
		Kind:        KindHTTPAPI,
		BaseURL:     origin.URL,
		BearerToken: "tok_secret_123456",
		Headers:     map[string]string{"X-Api-Secret": "hdr_secret_654321"},
		ReadOnly:    true,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Invoke(context.Background(), InvokeRequest{
		Action:   "http_request",
		Profile:  "redir-test",
		Method:   "GET",
		Endpoint: "/v1/items",
	}); err == nil || !strings.Contains(err.Error(), "cross-origin redirect") {
		t.Fatalf("expected cross-origin redirect to be refused, got err=%v", err)
	}
	if leakedHeader != "" {
		t.Fatalf("custom secret header leaked to cross-origin redirect target: %q", leakedHeader)
	}
}
