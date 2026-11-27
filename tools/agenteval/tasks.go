package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/subproc"
)

// Task defines one self-contained, real-toolchain coding benchmark task.
type Task struct {
	ID              string
	Language        string
	Category        string
	Description     string
	Prompt          string
	FollowUps       []string
	FirstTurnCheck  []string
	InitialHistory  []agent.Message
	RequireTool     string
	Files           map[string]string
	AcceptanceTests map[string]string
	VerifyCmd       []string
}

// BuiltinTasks returns the curated cross-language coding evaluation suite.
func BuiltinTasks() []Task {
	return []Task{
		taskGoRetryBackoff(),
		taskGoLRUCache(),
		taskGoCrossFileLimit(),
		taskJSSemverSort(),
		taskJSONConfigRepair(),
		taskPyRateLimiter(),
		taskKeyMigration(),
		taskReportReplay(),
	}
}

func taskGoRetryBackoff() Task {
	return Task{
		ID:          "go_retry_backoff",
		Language:    "go",
		Category:    "bugfix",
		Description: "Fix context cancellation during retry sleep and Retry-After parsing",
		RequireTool: "go",
		VerifyCmd:   []string{"go", "test", "-race", "-count=1", "./..."},
		Prompt: `Fix the bugs in this Go package so that all tests in retry_test.go pass under 'go test -race ./...'.
Inspect the source and test files first, fix the implementation in retry.go (do not weaken or delete tests), and run the test suite to verify.`,
		Files: map[string]string{
			"go.mod": "module evalretry\n\ngo 1.22\n",
			"retry.go": `package evalretry

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Do runs fn up to maxAttempts times with exponential backoff starting at baseDelay.
func Do(ctx context.Context, maxAttempts int, baseDelay time.Duration, fn func(attempt int) error) error {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = fn(attempt)
		if lastErr == nil {
			return nil
		}
		if attempt == maxAttempts {
			break
		}
		delay := baseDelay << (attempt - 1)
		time.Sleep(delay)
	}
	if lastErr == nil {
		return errors.New("retry exhausted")
	}
	return lastErr
}

// ParseRetryAfter parses an HTTP Retry-After header (delta-seconds or HTTP-date).
// Returns (duration, true) for valid headers; past HTTP-dates clamp to 0.
func ParseRetryAfter(header string, now time.Time) (time.Duration, bool) {
	v := strings.TrimSpace(header)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second, true
	}
	if ts, err := http.ParseTime(v); err == nil {
		return ts.Sub(now), true
	}
	return 0, false
}
`,
		},
		AcceptanceTests: map[string]string{
			"retry_test.go": `package evalretry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDoHonorsContextCancelDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	err := Do(ctx, 5, 5*time.Second, func(int) error {
		return errors.New("transient")
	})
	elapsed := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("Do ignored cancellation during backoff sleep (elapsed %v)", elapsed)
	}
}

func TestDoSucceedsOnSecondAttempt(t *testing.T) {
	attempts := 0
	err := Do(context.Background(), 3, time.Millisecond, func(n int) error {
		attempts = n
		if n < 2 {
			return errors.New("try again")
		}
		return nil
	})
	if err != nil || attempts != 2 {
		t.Fatalf("got err=%v attempts=%d, want nil / 2", err, attempts)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	if d, ok := ParseRetryAfter("12", now); !ok || d != 12*time.Second {
		t.Fatalf("delta seconds: got (%v, %v), want (12s, true)", d, ok)
	}
	if d, ok := ParseRetryAfter("0", now); !ok || d != 0 {
		t.Fatalf("zero seconds: got (%v, %v), want (0, true)", d, ok)
	}
	if _, ok := ParseRetryAfter("-5", now); ok {
		t.Fatalf("negative delta seconds must be rejected")
	}
	future := now.Add(45 * time.Second).UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT")
	if d, ok := ParseRetryAfter(future, now); !ok || d != 45*time.Second {
		t.Fatalf("future date: got (%v, %v), want (45s, true)", d, ok)
	}
	past := now.Add(-10 * time.Second).UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT")
	if d, ok := ParseRetryAfter(past, now); !ok || d != 0 {
		t.Fatalf("past date must clamp to 0, got (%v, %v)", d, ok)
	}
}
`,
		},
	}
}

func taskGoLRUCache() Task {
	return Task{
		ID:          "go_lru_cache",
		Language:    "go",
		Category:    "concurrency",
		Description: "Fix LRU recency promotion, update-at-capacity eviction, and concurrency race",
		RequireTool: "go",
		VerifyCmd:   []string{"go", "test", "-race", "-count=1", "./..."},
		Prompt: `Fix all bugs in lru.go so that lru_test.go passes under 'go test -race ./...'.
Ensure Get and existing-key Set update recency properly, updating an existing key at capacity never evicts another entry, and all operations are race-free under concurrent access. Do not weaken tests.`,
		Files: map[string]string{
			"go.mod": "module evallru\n\ngo 1.22\n",
			"lru.go": `package evallru

import "container/list"

type entry struct {
	key string
	val string
}

// Cache is a bounded LRU string cache.
type Cache struct {
	cap   int
	ll    *list.List
	items map[string]*list.Element
}

func New(capacity int) *Cache {
	if capacity < 1 {
		capacity = 1
	}
	return &Cache{
		cap:   capacity,
		ll:    list.New(),
		items: make(map[string]*list.Element, capacity),
	}
}

func (c *Cache) Get(key string) (string, bool) {
	el, ok := c.items[key]
	if !ok {
		return "", false
	}
	return el.Value.(*entry).val, true
}

func (c *Cache) Set(key, val string) {
	if c.ll.Len() >= c.cap {
		oldest := c.ll.Back()
		if oldest != nil {
			c.ll.Remove(oldest)
			delete(c.items, oldest.Value.(*entry).key)
		}
	}
	if el, ok := c.items[key]; ok {
		el.Value.(*entry).val = val
		return
	}
	el := c.ll.PushFront(&entry{key: key, val: val})
	c.items[key] = el
}

func (c *Cache) Delete(key string) {
	if el, ok := c.items[key]; ok {
		c.ll.Remove(el)
		delete(c.items, key)
	}
}

func (c *Cache) Len() int {
	return c.ll.Len()
}
`,
		},
		AcceptanceTests: map[string]string{
			"lru_test.go": `package evallru

import (
	"fmt"
	"sync"
	"testing"
)

func TestLRUEvictionAndPromotion(t *testing.T) {
	c := New(2)
	c.Set("a", "1")
	c.Set("b", "2")
	if v, ok := c.Get("a"); !ok || v != "1" {
		t.Fatalf("expected a=1, got %q %v", v, ok)
	}
	// Updating existing key at full capacity must not evict "b".
	c.Set("a", "1b")
	if c.Len() != 2 {
		t.Fatalf("len after update = %d, want 2", c.Len())
	}
	if v, ok := c.Get("b"); !ok || v != "2" {
		t.Fatalf("updating a evicted b: got %q %v", v, ok)
	}
	// Now "b" is most recent, so inserting "c" must evict "a".
	c.Set("c", "3")
	if _, ok := c.Get("a"); ok {
		t.Fatalf("expected a to be evicted")
	}
	if v, ok := c.Get("b"); !ok || v != "2" {
		t.Fatalf("expected b to remain, got %q %v", v, ok)
	}
	if v, ok := c.Get("c"); !ok || v != "3" {
		t.Fatalf("expected c=3, got %q %v", v, ok)
	}
}

func TestLRUConcurrentSafety(t *testing.T) {
	c := New(16)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				k := fmt.Sprintf("k-%d", (id+i)%24)
				c.Set(k, fmt.Sprintf("v-%d", i))
				c.Get(k)
				if i%7 == 0 {
					c.Delete(k)
				}
				_ = c.Len()
			}
		}(g)
	}
	wg.Wait()
}
`,
		},
	}
}

func taskGoCrossFileLimit() Task {
	return Task{
		ID:          "go_crossfile_limit",
		Language:    "go",
		Category:    "refactor",
		Description: "Wire MaxBytes limit across config.go, reader.go, and service.go",
		RequireTool: "go",
		VerifyCmd:   []string{"go", "test", "-count=1", "./..."},
		Prompt: `Wire a MaxBytes size guard across config.go, reader.go, and service.go so all tests in service_test.go pass:
1. Ensure Config supports MaxBytes (defaulting to 64*1024 when <= 0 via EffectiveMaxBytes()).
2. Update ReadLimited(r io.Reader, maxBytes int64) ([]byte, error) to return ErrTooLarge when the stream has more than maxBytes bytes (reading at most maxBytes+1 bytes).
3. Wire Service.Load(r io.Reader) to use Config.EffectiveMaxBytes() and ReadLimited.
Run 'go test ./...' to confirm everything passes.`,
		Files: map[string]string{
			"go.mod": "module evallimit\n\ngo 1.22\n",
			"config.go": `package evallimit

const DefaultMaxBytes int64 = 64 * 1024

type Config struct {
	Prefix string
}
`,
			"reader.go": `package evallimit

import (
	"errors"
	"io"
)

var ErrTooLarge = errors.New("payload exceeds max bytes")

func ReadLimited(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}
`,
			"service.go": `package evallimit

import "io"

type Service struct {
	cfg Config
}

func NewService(cfg Config) *Service {
	return &Service{cfg: cfg}
}

func (s *Service) Load(r io.Reader) (string, error) {
	data, err := ReadLimited(r)
	if err != nil {
		return "", err
	}
	return s.cfg.Prefix + string(data), nil
}
`,
		},
		AcceptanceTests: map[string]string{
			"service_test.go": `package evallimit

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestServiceRespectsExplicitMaxBytes(t *testing.T) {
	svc := NewService(Config{Prefix: "ok:", MaxBytes: 8})
	got, err := svc.Load(strings.NewReader("12345678"))
	if err != nil || got != "ok:12345678" {
		t.Fatalf("exact boundary failed: got=%q err=%v", got, err)
	}
	_, err = svc.Load(strings.NewReader("123456789"))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected ErrTooLarge, got %v", err)
	}
}

func TestServiceDefaultMaxBytes(t *testing.T) {
	svc := NewService(Config{})
	if svc.cfg.EffectiveMaxBytes() != DefaultMaxBytes {
		t.Fatalf("default = %d, want %d", svc.cfg.EffectiveMaxBytes(), DefaultMaxBytes)
	}
	oversize := bytes.Repeat([]byte("a"), int(DefaultMaxBytes)+1)
	if _, err := svc.Load(bytes.NewReader(oversize)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected ErrTooLarge on default cap, got %v", err)
	}
}
`,
		},
	}
}

func taskJSSemverSort() Task {
	return Task{
		ID:          "js_semver_sort",
		Language:    "javascript",
		Category:    "bugfix",
		Description: "Fix numeric segment comparison and prerelease precedence in ESM semver helper",
		RequireTool: "node",
		VerifyCmd:   []string{"node", "--test", "semver.test.mjs"},
		Prompt: `Fix the bugs in compare.mjs (and filter.mjs if needed) so that 'node --test' passes completely.
Follow standard SemVer 2.0.0 precedence: numeric major/minor/patch comparison, prerelease versions sort before their corresponding release (e.g. 1.0.0-alpha.1 < 1.0.0), and numeric prerelease identifiers compare numerically (alpha.2 < alpha.10). Do not edit semver.test.mjs.`,
		Files: map[string]string{
			"package.json": "{\n  \"name\": \"eval-semver\",\n  \"type\": \"module\"\n}\n",
			"compare.mjs": `export function compareSemver(a, b) {
  if (a === b) return 0;
  return a < b ? -1 : 1;
}
`,
			"filter.mjs": `import { compareSemver } from "./compare.mjs";

export function sortVersions(versions) {
  return [...versions].sort(compareSemver);
}

export function latestStable(versions) {
  const stable = versions.filter((v) => !v.includes("-"));
  if (stable.length === 0) return null;
  return sortVersions(stable)[stable.length - 1];
}
`,
		},
		AcceptanceTests: map[string]string{
			"semver.test.mjs": `import test from "node:test";
import assert from "node:assert/strict";
import { compareSemver } from "./compare.mjs";
import { latestStable, sortVersions } from "./filter.mjs";

test("sorts numeric minor/patch segments correctly", () => {
  assert.deepEqual(
    sortVersions(["1.10.0", "1.2.0", "1.2.10", "1.2.2"]),
    ["1.2.0", "1.2.2", "1.2.10", "1.10.0"]
  );
});

test("orders prerelease before release and compares numeric prerelease ids", () => {
  assert.equal(compareSemver("1.0.0-alpha.2", "1.0.0-alpha.10"), -1);
  assert.equal(compareSemver("1.0.0-rc.1", "1.0.0"), -1);
  assert.equal(compareSemver("1.0.0", "1.0.0-rc.1"), 1);
  assert.deepEqual(
    sortVersions(["1.0.0", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-alpha.1"]),
    ["1.0.0-alpha.1", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0"]
  );
});

test("picks latest stable version", () => {
  assert.equal(latestStable(["0.9.0", "0.10.0", "1.0.0-rc.1"]), "0.10.0");
});
`,
		},
	}
}

func taskJSONConfigRepair() Task {
	return Task{
		ID:          "json_config_repair",
		Language:    "json+js",
		Category:    "bugfix",
		Description: "Repair malformed JSON config and fix deep default merging without mutating defaults",
		RequireTool: "node",
		VerifyCmd:   []string{"node", "--test", "loader.test.mjs"},
		Prompt: `Fix the syntax error in config.json and the default-merging bugs in loader.mjs so 'node --test' passes.
Ensure loading config.json works, partial nested retry overrides preserve unspecified defaults (e.g. maxAttempts), and DEFAULTS is never mutated across calls. Do not modify loader.test.mjs.`,
		Files: map[string]string{
			"package.json": "{\n  \"name\": \"eval-json-config\",\n  \"type\": \"module\"\n}\n",
			"config.json": `{
  "service": "hoxi-worker",
  "retry": {
    "timeoutMs": 4500,
  }
}
`,
			"loader.mjs": `import { readFileSync } from "node:fs";

export const DEFAULTS = {
  service: "default",
  retry: {
    maxAttempts: 3,
    timeoutMs: 1000,
  },
};

export function mergeConfig(override) {
  return Object.assign(DEFAULTS, override);
}

export function loadConfig(filePath) {
  const parsed = JSON.parse(readFileSync(filePath, "utf8"));
  return mergeConfig(parsed);
}
`,
		},
		AcceptanceTests: map[string]string{
			"loader.test.mjs": `import test from "node:test";
import assert from "node:assert/strict";
import { DEFAULTS, loadConfig, mergeConfig } from "./loader.mjs";

test("loads valid config.json and preserves nested defaults", () => {
  const cfg = loadConfig(new URL("./config.json", import.meta.url));
  assert.equal(cfg.service, "hoxi-worker");
  assert.equal(cfg.retry.timeoutMs, 4500);
  assert.equal(cfg.retry.maxAttempts, 3);
});

test("does not mutate DEFAULTS across calls", () => {
  const first = mergeConfig({ service: "svc-a", retry: { maxAttempts: 7 } });
  assert.equal(first.retry.timeoutMs, 1000);
  assert.equal(DEFAULTS.service, "default");
  assert.equal(DEFAULTS.retry.maxAttempts, 3);
  assert.equal(DEFAULTS.retry.timeoutMs, 1000);
});
`,
		},
	}
}

func taskPyRateLimiter() Task {
	return Task{
		ID:          "py_rate_limiter",
		Language:    "python",
		Category:    "bugfix",
		Description: "Fix sliding-window expiry boundary and idle key pruning in Python rate limiter",
		RequireTool: "python3",
		VerifyCmd:   []string{"python3", "-m", "unittest", "-v", "test_limiter"},
		Prompt: `Fix the bugs in limiter.py so that 'python3 -m unittest discover -v' passes.
An event recorded at timestamp t must be considered expired when now - t >= window_seconds, and prune(now) must remove keys that have no remaining active timestamps so idle keys do not leak memory. Do not weaken test_limiter.py.`,
		Files: map[string]string{
			"limiter.py": `class SlidingWindowLimiter:
    def __init__(self, limit: int, window_seconds: float):
        self.limit = limit
        self.window_seconds = window_seconds
        self._events = {}

    def allow(self, key: str, now: float) -> bool:
        bucket = self._events.setdefault(key, [])
        cutoff = now - self.window_seconds
        bucket[:] = [ts for ts in bucket if ts >= cutoff]
        if len(bucket) >= self.limit:
            return False
        bucket.append(now)
        return True

    def prune(self, now: float) -> int:
        cutoff = now - self.window_seconds
        removed = 0
        for key, bucket in self._events.items():
            before = len(bucket)
            bucket[:] = [ts for ts in bucket if ts >= cutoff]
            removed += before - len(bucket)
        return removed

    def tracked_keys(self) -> int:
        return len(self._events)
`,
		},
		AcceptanceTests: map[string]string{
			"test_limiter.py": `import unittest
from limiter import SlidingWindowLimiter


class SlidingWindowLimiterTests(unittest.TestCase):
    def test_exact_window_boundary_expires(self):
        lim = SlidingWindowLimiter(limit=2, window_seconds=10.0)
        self.assertTrue(lim.allow("u1", 100.0))
        self.assertTrue(lim.allow("u1", 105.0))
        self.assertFalse(lim.allow("u1", 109.9))
        # At t=110.0, event at t=100.0 is exactly window_seconds old and must expire.
        self.assertTrue(lim.allow("u1", 110.0))

    def test_prune_drops_idle_keys(self):
        lim = SlidingWindowLimiter(limit=1, window_seconds=5.0)
        self.assertTrue(lim.allow("a", 1.0))
        self.assertTrue(lim.allow("b", 4.0))
        removed = lim.prune(6.0)
        self.assertEqual(removed, 1)
        self.assertEqual(lim.tracked_keys(), 1)
        lim.prune(10.0)
        self.assertEqual(lim.tracked_keys(), 0)


if __name__ == "__main__":
    unittest.main()
`,
		},
	}
}

// Materialize writes a task's initial repository files into dir.
func Materialize(task Task, dir string) error {
	if err := writeFixtureFiles(dir, task.Files); err != nil {
		return err
	}
	if err := writeFixtureFiles(dir, task.AcceptanceTests); err != nil {
		return err
	}
	initGitBaseline(dir)
	return nil
}

func writeFixtureFiles(dir string, files map[string]string) error {
	for rel, content := range files {
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", rel, err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	return nil
}

func initGitBaseline(dir string) {
	if _, err := exec.LookPath("git"); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runGit := func(args ...string) error {
		cmd := subproc.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = sanitizedEvalEnv()
		return cmd.Run()
	}
	if err := runGit("init", "-q"); err != nil {
		return
	}
	_ = runGit("config", "user.email", "eval@hoxi.local")
	_ = runGit("config", "user.name", "Hoxi Eval")
	if err := runGit("add", "."); err != nil {
		return
	}
	_ = runGit("commit", "-q", "-m", "initial fixture")
}

func sanitizedEvalEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "GIT_ASKPASS", "SSH_ASKPASS", "GIT_EDITOR", "GIT_TERMINAL_PROMPT", "GCM_INTERACTIVE":
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
}
