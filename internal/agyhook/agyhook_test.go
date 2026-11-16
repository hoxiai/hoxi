package agyhook

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func runHook(t *testing.T, env map[string]string, stdin string) Decision {
	t.Helper()
	var out bytes.Buffer
	if code := Run(strings.NewReader(stdin), &out, envMap(env)); code != 0 {
		t.Fatalf("hook exit code = %d, want 0", code)
	}
	var d Decision
	if err := json.Unmarshal(out.Bytes(), &d); err != nil {
		t.Fatalf("hook output is not JSON: %q", out.String())
	}
	return d
}

func TestPassthroughWithoutHoxiEnv(t *testing.T) {
	for _, env := range []map[string]string{
		{},
		{EnvSessionID: "ses_agy_1", EnvHookURL: "http://127.0.0.1:1/x"},
		{EnvHookURL: "http://127.0.0.1:1/x", EnvHookToken: "tok"},
	} {
		if d := runHook(t, env, `{}`); d.Decision != "allow" {
			t.Fatalf("env %v: decision = %q, want allow", env, d.Decision)
		}
	}
}

func TestDeniesNonLoopbackURL(t *testing.T) {
	env := map[string]string{EnvSessionID: "s", EnvHookToken: "t", EnvHookURL: "http://example.com/hook"}
	if d := runHook(t, env, `{}`); d.Decision != "deny" {
		t.Fatalf("decision = %q, want deny", d.Decision)
	}
}

func TestForwardsPayloadAndRelaysDecision(t *testing.T) {
	var gotAuth, gotSession, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotSession = r.Header.Get(HeaderSession)
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		_, _ = w.Write([]byte(`{"decision":"auto_approve","reason":"ok"}`))
	}))
	defer srv.Close()
	env := map[string]string{EnvSessionID: "ses_agy_9", EnvHookToken: "tok-1", EnvHookURL: srv.URL + "/hook"}
	d := runHook(t, env, `{"toolCall":{"name":"run_command"}}`)
	if d.Decision != "auto_approve" {
		t.Fatalf("decision = %q, want auto_approve", d.Decision)
	}
	if gotAuth != "Bearer tok-1" || gotSession != "ses_agy_9" || !strings.Contains(gotBody, "run_command") {
		t.Fatalf("forwarded auth=%q session=%q body=%q", gotAuth, gotSession, gotBody)
	}
}

func TestFailsClosedOnGateErrors(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"status":  func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) },
		"garbage": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`not json`)) },
		"unknown": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"decision":"yolo"}`)) },
	}
	for name, h := range cases {
		srv := httptest.NewServer(h)
		env := map[string]string{EnvSessionID: "s", EnvHookToken: "t", EnvHookURL: srv.URL}
		if d := runHook(t, env, `{}`); d.Decision != "deny" {
			t.Fatalf("%s: decision = %q, want deny", name, d.Decision)
		}
		srv.Close()
	}
	env := map[string]string{EnvSessionID: "s", EnvHookToken: "t", EnvHookURL: "http://127.0.0.1:1/unreachable"}
	if d := runHook(t, env, `{}`); d.Decision != "deny" {
		t.Fatalf("unreachable: decision = %q, want deny", d.Decision)
	}
}
