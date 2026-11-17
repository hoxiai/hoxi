package dataprofiles

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

	// Verify SSRF / host-override guard rejects external URLs and protocol-relative paths.
	for _, badEP := range []string{"https://attacker.invalid/steal", "//attacker.invalid/steal", "api/v1/nodes"} {
		if _, err := svc.Invoke(ctx, InvokeRequest{
			Action:   "http_request",
			Profile:  "gopanel-prod",
			Method:   "GET",
			Endpoint: badEP,
		}); err == nil {
			t.Fatalf("expected endpoint %q to be rejected", badEP)
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
