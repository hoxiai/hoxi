package diagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) *store {
	t.Helper()
	return &store{directory: filepath.Join(t.TempDir(), "logs"), budget: maxBytes, segment: segmentBytes, now: time.Now}
}

func TestSanitize(t *testing.T) {
	for _, value := range []string{"Bearer private-value", "api_key=private-value", "token=private-value", "https://user:private-value@host/path?q=secret", "/Users/alice/private-value", "prompt=private-value", "output:private-value", "headers=private-value", "sk-private-value", "PW=private-value", "DB_PASS=private-value", "client_secret=private-value", "mysql -uroot -pprivate-value", "curl -u admin:private-value x", "postgres://app:private-value@db/app"} {
		e := sanitize(Entry{Message: value, ModelID: value})
		b, _ := json.Marshal(e)
		if bytes.Contains(b, []byte("private-value")) {
			t.Errorf("leaked %q: %s", value, b)
		}
	}
	if got := sanitize(Entry{Message: "pwd failed: exit 1"}).Message; got != "pwd failed: exit 1" {
		t.Errorf("ordinary text was redacted: %q", got)
	}
	e := sanitize(Entry{Message: strings.Repeat("a", 20000), SessionID: strings.Repeat("b", 20000)})
	if len(e.Message) > 256 || len(e.SessionID) > 128 {
		t.Fatal("unbounded entry")
	}
}

func TestStorePermissionsRotationAndRetention(t *testing.T) {
	s := testStore(t)
	s.segment, s.budget = 450, 1000
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	for i := 0; i < 10; i++ {
		s.record(Entry{Message: strings.Repeat("x", 100)})
		if s.err != nil {
			t.Fatal(s.err)
		}
		now = now.Add(time.Millisecond)
	}
	files, err := s.files()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 2 {
		t.Fatal("did not rotate")
	}
	var total int64
	for _, f := range files {
		total += f.size
		info, err := os.Stat(f.path)
		if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0600) {
			t.Fatalf("file permissions: %v %v", info, err)
		}
		if f.size > s.segment {
			t.Fatal("oversized segment")
		}
	}
	if total > s.budget {
		t.Fatalf("budget exceeded: %d", total)
	}
	info, _ := os.Stat(s.directory)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
		t.Fatal("directory permissions")
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	for _, f := range files {
		if err := os.Chtimes(f.path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.prune(0); err != nil {
		t.Fatal(err)
	}
	files, _ = s.files()
	if len(files) != 0 {
		t.Fatal("expired files retained")
	}
}

func TestRecordsWithinOneClockTickStayNewestFirst(t *testing.T) {
	s := testStore(t)
	frozen := time.Now().UTC()
	s.now = func() time.Time { return frozen }
	s.record(Entry{Message: "first"})
	s.record(Entry{Message: "second"})
	entries, err := s.entries(filter{limit: 10})
	if err != nil || len(entries) != 2 {
		t.Fatalf("%v %v", entries, err)
	}
	if entries[0].Message != "second" || entries[1].Message != "first" {
		t.Fatalf("order within one clock tick: %v", entries)
	}
	if entries[0].ID <= entries[1].ID {
		t.Fatalf("record IDs must increase: %s %s", entries[1].ID, entries[0].ID)
	}
}

func TestFilteringAndHTTP(t *testing.T) {
	s := testStore(t)
	s.record(Entry{Kind: "request", Level: "error", Source: "provider", Message: "request failed", SessionID: "s1", ProviderID: "p1"})
	s.record(Entry{Kind: "system", Message: "system started", SessionID: "s2"})
	for _, query := range []string{"kind=request", "level=error", "level=ERROR", "level=eRrOr", "sessionID=s1", "providerID=p1", "q=FAILED", "limit=1"} {
		w := httptest.NewRecorder()
		s.serveHTTP(w, httptest.NewRequest("GET", "/api/hoxi/diagnostics?"+query, nil))
		var body struct {
			Entries []Entry `json:"entries"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || len(body.Entries) != 1 {
			t.Fatalf("%s: %d %s", query, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/hoxi/diagnostics", "/api/hoxi/diagnostics/export"} {
		for _, method := range []string{"POST", "PUT", "DELETE", "PATCH", "HEAD"} {
			w := httptest.NewRecorder()
			s.serveHTTP(w, httptest.NewRequest(method, path, nil))
			if w.Code != 405 || w.Header().Get("Allow") != "GET" {
				t.Fatalf("%s %s: %d", method, path, w.Code)
			}
		}
	}
	w := httptest.NewRecorder()
	s.serveHTTP(w, httptest.NewRequest("GET", "/api/hoxi/diagnostics/export", nil))
	if w.Code != 200 || w.Header().Get("Content-Disposition") == "" || !strings.Contains(w.Body.String(), `"version":1`) {
		t.Fatal(w.Body.String())
	}
	if strings.Contains(w.Body.String(), s.directory) || strings.Contains(w.Body.String(), `"directory"`) {
		t.Fatal("export exposes directory")
	}
	w = httptest.NewRecorder()
	s.serveHTTP(w, httptest.NewRequest("GET", "/api/hoxi/diagnostics?limit=invalid", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatal("invalid limit accepted")
	}
}

func TestLevelNormalization(t *testing.T) {
	for _, level := range []string{"error", "WARN", "iNfO", "debug"} {
		if got := sanitize(Entry{Level: level}).Level; got != strings.ToUpper(level) {
			t.Fatalf("%s => %s", level, got)
		}
	}
	s := testStore(t)
	if err := s.prepare(); err != nil {
		t.Fatal(err)
	}
	// Existing lowercase records must be normalized on read as well.
	b, _ := json.Marshal(Entry{Time: time.Now(), Kind: "request", Level: "error"})
	if err := os.WriteFile(filepath.Join(s.directory, "request-old.jsonl"), append(b, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := s.entries(filter{level: "ERROR", limit: 10})
	if err != nil || len(entries) != 1 || entries[0].Level != "ERROR" {
		t.Fatalf("%v %v", entries, err)
	}
}

func TestOpenDirectory(t *testing.T) {
	s := testStore(t)
	calls := 0
	s.openDirectory = func(path string) error {
		calls++
		if path != s.directory {
			t.Fatalf("untrusted directory: %s", path)
		}
		return nil
	}
	for _, method := range []string{"GET", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"} {
		w := httptest.NewRecorder()
		s.serveHTTP(w, httptest.NewRequest(method, "/api/hoxi/diagnostics/open-directory", nil))
		if w.Code != 405 || w.Header().Get("Allow") != "POST" {
			t.Fatalf("%s: %d", method, w.Code)
		}
	}
	if calls != 0 {
		t.Fatal("opener called for wrong method")
	}
	w := httptest.NewRecorder()
	s.serveHTTP(w, httptest.NewRequest("POST", "/api/hoxi/diagnostics/open-directory?directory=/evil&command=evil", strings.NewReader(`{"directory":"/evil"}`)))
	if w.Code != 200 || calls != 1 {
		t.Fatalf("%d %d", w.Code, calls)
	}
	s.openDirectory = func(string) error { return errors.New("private child failure") }
	w = httptest.NewRecorder()
	s.serveHTTP(w, httptest.NewRequest("POST", "/api/hoxi/diagnostics/open-directory", nil))
	if w.Code != 500 || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestSlogStaticEventsAndExitCode(t *testing.T) {
	s := testStore(t)
	global.Lock()
	previous := global.s
	global.s = s
	global.Unlock()
	t.Cleanup(func() { global.Lock(); global.s = previous; global.Unlock() })
	logger := slog.New(NewHandler(nil)).With("exitCode", 17)
	logger.Error("failed to start opencode daemon", "error", errors.New("private prompt echoed"), "output", "private output")
	logger.Error("failed to start opencode daemon: private prompt")
	entries, err := s.entries(filter{limit: 10})
	if err != nil || len(entries) != 2 {
		t.Fatalf("%v %v", entries, err)
	}
	if entries[0].Message != "system log event" || entries[1].Message != "failed to start opencode daemon" {
		t.Fatalf("%v", entries)
	}
	if entries[1].ExitCode == nil || *entries[1].ExitCode != 17 {
		t.Fatal("exit code missing")
	}
	b, _ := json.Marshal(entries)
	if strings.Contains(string(b), "private") {
		t.Fatalf("free text leaked: %s", b)
	}
}

func TestSlogDoesNotPersistFreeText(t *testing.T) {
	s := testStore(t)
	global.Lock()
	previous := global.s
	global.s = s
	global.Unlock()
	t.Cleanup(func() { global.Lock(); global.s = previous; global.Unlock() })
	var stderr bytes.Buffer
	logger := slog.New(NewHandler(&stderr)).With("prompt", "private-prompt", "source", "test")
	logger.ErrorContext(context.Background(), "private-output", "error", "private-error", "headers", "private-headers")
	if !strings.Contains(stderr.String(), "private-output") {
		t.Fatal("stderr behavior changed")
	}
	entries, err := s.entries(filter{limit: 10})
	if err != nil || len(entries) != 1 {
		t.Fatalf("%v %v", entries, err)
	}
	b, _ := json.Marshal(entries)
	if bytes.Contains(b, []byte("private-")) {
		t.Fatalf("free text leaked: %s", b)
	}
	if entries[0].Source != "test" {
		t.Fatal("source missing")
	}
}

func TestRejectSymlinkDirectory(t *testing.T) {
	s := testStore(t)
	if err := os.Symlink(t.TempDir(), s.directory); err != nil {
		t.Fatal(err)
	}
	s.record(Entry{Message: "test"})
	if s.err == nil {
		t.Fatal("symlink accepted")
	}
}

// Test binaries record fake models and failures; without HOXI_HOME they must
// write to a temp directory, never to the user's real ~/.hoxi/logs.
func TestInitializeKeepsTestBinariesOutOfRealLogs(t *testing.T) {
	t.Setenv("HOXI_HOME", "")
	if u, err := user.Current(); err == nil && strings.TrimSpace(u.HomeDir) != "" {
		t.Setenv("HOME", u.HomeDir)
	}
	global.Lock()
	previous := global.s
	global.s = nil
	global.Unlock()
	t.Cleanup(func() { global.Lock(); global.s = previous; global.Unlock() })

	_ = Initialize()
	global.Lock()
	dir := global.s.directory
	global.Unlock()
	if want := filepath.Join(os.TempDir(), "hoxi-test-diagnostics"); dir != want {
		t.Fatalf("test diagnostics dir = %q, want %q", dir, want)
	}
	if underRealHoxiHome(dir) {
		t.Fatalf("test diagnostics must not write under the real ~/.hoxi: %q", dir)
	}

	// A test that points HOME at a temp directory keeps its own logs.
	home := t.TempDir()
	t.Setenv("HOME", home)
	global.Lock()
	global.s = nil
	global.Unlock()
	_ = Initialize()
	global.Lock()
	dir = global.s.directory
	global.Unlock()
	if want := filepath.Join(home, ".hoxi", "logs"); dir != want {
		t.Fatalf("diagnostics dir with a temp HOME = %q, want %q", dir, want)
	}
}

func TestSlogSkipsUnreviewedInfoWritesToDisk(t *testing.T) {
	s := testStore(t)
	global.Lock()
	previous := global.s
	global.s = s
	global.Unlock()
	t.Cleanup(func() { global.Lock(); global.s = previous; global.Unlock() })

	var stderr bytes.Buffer
	logger := slog.New(NewHandler(&stderr))
	logger.Info("routine startup chatter", "detail", "visible-on-stderr")
	logger.Info("hoxi headless server is ready")
	logger.Warn("unreviewed warning")

	if !strings.Contains(stderr.String(), "routine startup chatter") {
		t.Fatalf("unreviewed Info should still write to stderr: %s", stderr.String())
	}
	entries, err := s.entries(filter{limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 persisted entries (static Info + Warn), got %d: %+v", len(entries), entries)
	}
	if entries[0].Message != "system log event" || entries[0].Level != "WARN" {
		t.Fatalf("unexpected newest entry: %+v", entries[0])
	}
	if entries[1].Message != "hoxi headless server is ready" || entries[1].Level != "INFO" {
		t.Fatalf("unexpected static Info entry: %+v", entries[1])
	}
}

func TestQuietHTTPServerLoggerFiltering(t *testing.T) {
	s := testStore(t)
	global.Lock()
	previous := global.s
	global.s = s
	global.Unlock()
	prevLogger := slog.Default()
	var stderr bytes.Buffer
	slog.SetDefault(slog.New(NewHandler(&stderr)))
	t.Cleanup(func() {
		slog.SetDefault(prevLogger)
		global.Lock()
		global.s = previous
		global.Unlock()
	})

	httpLog := NewQuietHTTPServerLogger()
	for _, benign := range []string{
		"http: proxy error: context canceled",
		"write tcp 127.0.0.1:5400->127.0.0.1:51234: write: broken pipe",
		"read tcp: connection reset by peer",
		"accept tcp 127.0.0.1:5400: use of closed network connection",
		"http: proxy error: EOF",
		"http: proxy error: unexpected EOF",
		"http: superfluous response.WriteHeader call",
	} {
		httpLog.Print(benign)
	}
	entries, err := s.entries(filter{limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected benign HTTP disconnect logs to be suppressed, got: %+v", entries)
	}

	// Substring "eof" inside ordinary words (e.g. geofence/typeof) must NOT be suppressed.
	httpLog.Print("http: proxy error: geofence check failed with Bearer sk-secret-token")
	entries, err = s.entries(filter{limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected non-benign HTTP error to be recorded, got %d: %+v", len(entries), entries)
	}
	if entries[0].Message != "http server error" || entries[0].Source != "http" || entries[0].Level != "WARN" {
		t.Fatalf("unexpected HTTP error entry: %+v", entries[0])
	}
	if strings.Contains(stderr.String(), "sk-secret-token") {
		t.Fatalf("secret leaked in quiet HTTP logger stderr: %s", stderr.String())
	}
}

func TestActiveSegmentCacheInvalidatesOnPruneAndSymlink(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC()
	s.now = func() time.Time { return now }

	s.record(Entry{Message: "first"})
	if s.err != nil || s.activePath == "" {
		t.Fatalf("expected activePath cached after first record, err=%v path=%q", s.err, s.activePath)
	}
	firstPath := s.activePath

	// Fast path reuses same segment within 1 minute.
	now = now.Add(time.Second)
	s.record(Entry{Message: "second"})
	if s.err != nil || s.activePath != firstPath {
		t.Fatalf("expected fast path to reuse %q, got %q err=%v", firstPath, s.activePath, s.err)
	}

	// Replacing active segment with a symlink must be caught on the fast path.
	if runtime.GOOS != "windows" {
		target := filepath.Join(t.TempDir(), "outside.jsonl")
		if err := os.WriteFile(target, nil, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(firstPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, firstPath); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Second)
		s.record(Entry{Message: "third"})
		if s.err == nil {
			t.Fatal("expected symlinked active segment to be rejected")
		}
		_ = os.Remove(firstPath)
	}

	// Fresh record after cleanup succeeds, and prune invalidates cached activePath when removed.
	now = now.Add(time.Second)
	s.record(Entry{Message: "fourth"})
	if s.err != nil || s.activePath == "" {
		t.Fatalf("expected fresh record to succeed, err=%v path=%q", s.err, s.activePath)
	}
	old := now.Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(s.activePath, old, old); err != nil {
		t.Fatal(err)
	}
	if err := s.prune(0); err != nil {
		t.Fatal(err)
	}
	if s.activePath != "" || s.totalBytes != 0 {
		t.Fatalf("expected prune to invalidate removed activePath and totalBytes, got path=%q total=%d", s.activePath, s.totalBytes)
	}
}
