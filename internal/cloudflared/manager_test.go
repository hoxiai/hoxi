package cloudflared

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeHandle struct {
	done chan struct{}
	once sync.Once
}

func (h *fakeHandle) close()      { h.once.Do(func() { close(h.done) }) }
func (h *fakeHandle) Wait() error { <-h.done; return nil }
func (h *fakeHandle) Kill() error { h.close(); return nil }

// fakeRun reports a registered connection, then blocks until the context is
// cancelled so the supervisor sees a long-lived process.
func fakeRun(ctx context.Context, _ string, _ []string, _ []string, onLine func(string)) (handle, error) {
	h := &fakeHandle{done: make(chan struct{})}
	go func() {
		onLine("INF Registered tunnel connection connIndex=0")
		<-ctx.Done()
		h.close()
	}()
	return h, nil
}

func waitFor(t *testing.T, fn func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestStoreWritesWith0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cloudflared.json")
	store := NewStore(path)
	if err := store.Save(Config{Enabled: true, Token: "secret", Hostname: "abc.node.hoxi.work"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perms = %o, want 600", perm)
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Enabled || cfg.Token != "secret" || cfg.Hostname != "abc.node.hoxi.work" {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestManagerDisabledWithoutConfig(t *testing.T) {
	m := NewManager(Options{
		Store:   NewStore(filepath.Join(t.TempDir(), "cloudflared.json")),
		Resolve: func() string { return "" },
		Run:     fakeRun,
	})
	m.Start()
	if st := m.Status(); st.State != StateDisabled || st.Enabled {
		t.Fatalf("status = %+v, want disabled", st)
	}
}

func TestManagerReportsMissingBinary(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "cloudflared.json"))
	if err := store.Save(Config{Enabled: true, Token: "token"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	m := NewManager(Options{Store: store, Resolve: func() string { return "" }, Run: fakeRun})
	m.Start()
	if !waitFor(t, func() bool { return m.Status().State == StateMissing }) {
		t.Fatalf("status = %+v, want missing", m.Status())
	}
}

func TestManagerEnableConnectsAndDisableStops(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "cloudflared.json"))
	m := NewManager(Options{
		Store:   store,
		Resolve: func() string { return "/usr/local/bin/cloudflared" },
		Run:     fakeRun,
	})
	if err := m.Enable("cf-connector-token", "abc.node.hoxi.work"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !waitFor(t, func() bool { return m.Status().State == StateOnline }) {
		t.Fatalf("status = %+v, want online", m.Status())
	}
	st := m.Status()
	if !st.Enabled || !st.HasToken || st.Hostname != "abc.node.hoxi.work" {
		t.Fatalf("status = %+v", st)
	}
	if m.Hostname() != "abc.node.hoxi.work" {
		t.Fatalf("Hostname() = %q", m.Hostname())
	}
	// The connector token must never appear in the status payload.
	raw, _ := json.Marshal(st)
	if strings.Contains(string(raw), "cf-connector-token") {
		t.Fatal("status leaked the connector token")
	}

	if err := m.Disable(); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if st := m.Status(); st.State != StateDisabled || st.Enabled {
		t.Fatalf("status after disable = %+v", st)
	}
	if got := m.Hostname(); got != "" {
		t.Fatalf("Hostname() after disable = %q, want empty", got)
	}
}

func TestManagerNormalizesHostnameURL(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "cloudflared.json"))
	m := NewManager(Options{
		Store:   store,
		Resolve: func() string { return "/usr/local/bin/cloudflared" },
		Run:     fakeRun,
	})
	t.Cleanup(m.Stop)
	if err := m.Enable("cf-token", "  https://abc.node.hoxi.work/path/  "); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if got := m.Hostname(); got != "abc.node.hoxi.work" {
		t.Fatalf("Hostname() = %q, want abc.node.hoxi.work", got)
	}
	if got := m.Status().Hostname; got != "abc.node.hoxi.work" {
		t.Fatalf("Status().Hostname = %q, want abc.node.hoxi.work", got)
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Hostname != "abc.node.hoxi.work" {
		t.Fatalf("persisted hostname = %q, want abc.node.hoxi.work", cfg.Hostname)
	}
}

func TestStoreBacksUpCorruptConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cloudflared.json")
	if err := os.WriteFile(path, []byte("{corrupt-json"), 0o600); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}
	store := NewStore(path)
	if _, err := store.Load(); err == nil {
		t.Fatal("expected error loading corrupt config")
	}
	matches, err := filepath.Glob(path + ".corrupt-*")
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected 1 corrupt backup file, got %v (err=%v)", matches, err)
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil || string(raw) != "{corrupt-json" {
		t.Fatalf("corrupt backup = %q (err=%v), want {corrupt-json", string(raw), err)
	}
}

func TestLineWriterCapsUnboundedLines(t *testing.T) {
	var lines []string
	w := &lineWriter{onLine: func(s string) { lines = append(lines, s) }}
	chunk := strings.Repeat("x", maxLineBufferBytes+100)
	if _, err := w.Write([]byte(chunk)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(w.buf) > maxLineBufferBytes {
		t.Fatalf("len(w.buf) = %d, exceeds maxLineBufferBytes %d", len(w.buf), maxLineBufferBytes)
	}
	if len(lines) != 1 || len(lines[0]) != maxLineBufferBytes {
		t.Fatalf("flushed lines = %d (first len %d), want 1 line of %d bytes", len(lines), len(lines[0]), maxLineBufferBytes)
	}
}

func TestManagerEnableRejectsEmptyToken(t *testing.T) {
	m := NewManager(Options{
		Store:   NewStore(filepath.Join(t.TempDir(), "cloudflared.json")),
		Resolve: func() string { return "" },
		Run:     fakeRun,
	})
	if err := m.Enable("  ", "abc.node.hoxi.work"); err == nil {
		t.Fatal("expected an error for an empty token")
	}
}
