package ocapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestClientInjectsAuthAndDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != Username || pass != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/session":
			if r.URL.Query().Get("directory") != "/tmp/wt" || r.Header.Get("Content-Type") != "application/json" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"id":"ses_1"}`))
		default:
			w.WriteHeader(http.StatusTeapot)
			_, _ = w.Write([]byte(" nope "))
		}
	}))
	defer srv.Close()

	c := New(srv.URL+"/", "secret")
	var out struct{ ID string }
	if err := c.JSON(context.Background(), http.MethodPost, "/session", url.Values{"directory": {"/tmp/wt"}}, map[string]string{"title": "x"}, &out); err != nil || out.ID != "ses_1" {
		t.Fatalf("JSON() = %+v, %v", out, err)
	}
	err := c.JSON(context.Background(), http.MethodGet, "/missing", nil, nil, nil)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusTeapot || httpErr.Body != "nope" {
		t.Fatalf("expected HTTPError 418, got %v", err)
	}
	status, raw, err := c.Raw(context.Background(), http.MethodGet, "/missing", nil, nil)
	if err != nil || status != http.StatusTeapot || strings.TrimSpace(string(raw)) != "nope" {
		t.Fatalf("Raw() = %d %q %v", status, raw, err)
	}
}

func TestUnavailableClient(t *testing.T) {
	var nilClient *Client
	for _, c := range []*Client{nilClient, NewForPort(0, "x"), New("", "x")} {
		if c.Available() {
			t.Fatal("client without endpoint must be unavailable")
		}
		if err := c.JSON(context.Background(), http.MethodGet, "/x", nil, nil, nil); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("expected ErrUnavailable, got %v", err)
		}
	}
	if got := NewForPort(4096, "").URL("/session/status?x=1", url.Values{"directory": {"/a b"}}); got != "http://127.0.0.1:4096/session/status?x=1&directory=%2Fa+b" {
		t.Fatalf("unexpected URL %s", got)
	}
	if Transport().Proxy != nil {
		t.Fatal("loopback transport must bypass system proxies")
	}
}
