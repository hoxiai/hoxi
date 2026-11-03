package app

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/statusbar"
	"github.com/wailsapp/wails/v2/pkg/options"
)

func TestChooseFreePort(t *testing.T) {
	port, err := chooseFreePort()
	if err != nil {
		t.Fatalf("chooseFreePort returned error: %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Fatalf("chooseFreePort returned invalid port: %d", port)
	}
}

func TestGenerateRandomHex(t *testing.T) {
	h1, err := generateRandomHex(16)
	if err != nil {
		t.Fatalf("generateRandomHex error: %v", err)
	}
	if len(h1) != 32 {
		t.Fatalf("expected hex length 32, got %d (%q)", len(h1), h1)
	}
	h2, err := generateRandomHex(16)
	if err != nil {
		t.Fatalf("generateRandomHex second call error: %v", err)
	}
	if h1 == h2 {
		t.Fatalf("expected two random 16-byte hex strings to differ, got %q", h1)
	}
}

func TestEnvOr(t *testing.T) {
	const key = "HOXI_TEST_ENV_OR_KEY"
	t.Setenv(key, "")
	if got := envOr(key, "default-val"); got != "default-val" {
		t.Fatalf("expected fallback for empty env, got %q", got)
	}

	t.Setenv(key, "   ")
	if got := envOr(key, "default-val"); got != "default-val" {
		t.Fatalf("expected fallback for whitespace env, got %q", got)
	}

	t.Setenv(key, "  custom-val  ")
	if got := envOr(key, "default-val"); got != "custom-val" {
		t.Fatalf("expected trimmed env value 'custom-val', got %q", got)
	}
}

func TestDesktopAppLifecycleMethods(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tracker := statusbar.NewTracker()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	srv := &http.Server{
		Handler: http.NewServeMux(),
	}
	go func() {
		_ = srv.Serve(ln)
	}()

	dapp := &DesktopApp{
		cfg: Config{
			Address:  ln.Addr().String(),
			OpenCode: "/usr/bin/false",
			RootDir:  t.TempDir(),
		},
		logger:   logger,
		status:   tracker,
		wsServer: srv,
	}

	// onSecondInstance with nil context should be a safe no-op
	dapp.onSecondInstance(options.SecondInstanceData{})

	// killChild when child is nil should be a safe no-op
	dapp.killChild()
	dapp.childStopped = false

	// Start a dummy long-running sleep process and register via setChild
	cmd := startChild(t, "sleep", "30")
	dapp.setChild(cmd)
	if dapp.child != cmd {
		t.Fatal("expected dapp.child to be set")
	}

	// killChild should terminate the process and clear dapp.child
	dapp.killChild()
	if dapp.child != nil {
		t.Fatal("expected dapp.child to be nil after killChild")
	}
	<-dapp.childDone

	// Start another dummy process to verify shutdown cleans up both wsServer and child
	cmd2 := startChild(t, "sleep", "30")
	dapp.childStopped = false
	dapp.setChild(cmd2)

	dapp.startup(context.Background())
	dapp.shutdown(context.Background())
	if dapp.child != nil {
		t.Fatal("expected shutdown to clear child process")
	}
	<-dapp.childDone

	// BootstrapOpenCodeAsync with an immediately cancelled context and failing binary returns cleanly
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dapp.BootstrapOpenCodeAsync(ctx, 0, "testpass")
}

func TestResolveIdentity(t *testing.T) {
	t.Setenv("HOXI_VARIANT", "")

	def := ResolveIdentity(BuildVariant, "/Users/dev/code/hoxi/build/bin/Hoxi.app/Contents/MacOS/Hoxi")
	if def.Variant != "dev" || def.Title != "Hoxi Dev" || def.BundleID != "ai.hoxi.desktop.dev" || def.DefaultBridgeAddr != "127.0.0.1:5420" {
		t.Fatalf("expected default BuildVariant to resolve to dev identity for wails dev, got: %+v", def)
	}

	rel := ResolveIdentity("release", "/Applications/Hoxi.app/Contents/MacOS/Hoxi")
	if rel.Variant != "release" || rel.Title != "Hoxi" || rel.BundleID != "ai.hoxi.desktop" || rel.DefaultBridgeAddr != "127.0.0.1:5410" {
		t.Fatalf("unexpected release identity: %+v", rel)
	}

	devByFlag := ResolveIdentity("dev", "/Applications/Hoxi.app/Contents/MacOS/Hoxi")
	if devByFlag.Variant != "dev" || devByFlag.Title != "Hoxi Dev" || devByFlag.BundleID != "ai.hoxi.desktop.dev" || devByFlag.DefaultBridgeAddr != "127.0.0.1:5420" {
		t.Fatalf("unexpected dev identity from variant flag: %+v", devByFlag)
	}

	devByPath := ResolveIdentity("release", "/Users/dev/build/bin/Hoxi Dev.app/Contents/MacOS/Hoxi")
	if devByPath.Variant != "dev" || devByPath.BundleID != "ai.hoxi.desktop.dev" {
		t.Fatalf("unexpected dev identity from app bundle path: %+v", devByPath)
	}

	t.Setenv("HOXI_VARIANT", "dev")
	devByEnv := ResolveIdentity("release", "/Applications/Hoxi.app/Contents/MacOS/Hoxi")
	if devByEnv.Variant != "dev" || devByEnv.BundleID != "ai.hoxi.desktop.dev" {
		t.Fatalf("unexpected dev identity from env: %+v", devByEnv)
	}
}

func TestSanitizeFrontendDevServerEnv(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	liveURL := "http://" + ln.Addr().String() + "/"
	t.Setenv("frontenddevserverurl", liveURL)
	sanitizeFrontendDevServerEnv(logger, 200*time.Millisecond)
	if got := os.Getenv("frontenddevserverurl"); got != liveURL {
		t.Fatalf("expected live frontenddevserverurl %q to be preserved, got %q", liveURL, got)
	}

	_ = ln.Close()
	sanitizeFrontendDevServerEnv(logger, 150*time.Millisecond)
	if got := os.Getenv("frontenddevserverurl"); got != "" {
		t.Fatalf("expected dead frontenddevserverurl to be cleared, got %q", got)
	}
}

func TestAssetServerMiddlewareFallsBackOnBadGateway(t *testing.T) {
	gw := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Served-By", "gateway")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("gateway:" + r.URL.Path))
	})
	mw := assetServerMiddleware(gw)

	// 1. /api/* goes directly to gateway without hitting next.
	nextCalled := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/hoxi/bootstrap", nil))
	if nextCalled || rec.Code != http.StatusOK || rec.Body.String() != "gateway:/api/hoxi/bootstrap" {
		t.Fatalf("unexpected /api/ response: code=%d body=%q nextCalled=%v", rec.Code, rec.Body.String(), nextCalled)
	}

	// 2. 502 Bad Gateway from dead external Vite proxy falls back to gateway assets.
	h502 := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	rec502 := httptest.NewRecorder()
	h502.ServeHTTP(rec502, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec502.Code != http.StatusOK || rec502.Body.String() != "gateway:/" || rec502.Header().Get("X-Served-By") != "gateway" {
		t.Fatalf("expected 502 fallback to gateway, got code=%d body=%q", rec502.Code, rec502.Body.String())
	}

	// 3. Healthy 200 OK from external Vite proxy passes through untouched.
	h200 := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Served-By", "vite")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("vite-index"))
	}))
	rec200 := httptest.NewRecorder()
	h200.ServeHTTP(rec200, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec200.Code != http.StatusOK || rec200.Body.String() != "vite-index" || rec200.Header().Get("X-Served-By") != "vite" {
		t.Fatalf("expected healthy vite response, got code=%d body=%q", rec200.Code, rec200.Body.String())
	}

	// 4. 502 via http.Error (which sets Content-Type: text/plain + X-Content-Type-Options: nosniff)
	// must discard failed response headers so http.FileServer serves .js with JavaScript MIME type.
	assetDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(assetDir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetDir, "assets", "app.js"), []byte("console.log('ok');\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fileServerMW := assetServerMiddleware(http.FileServer(http.Dir(assetDir)))
	h502JS := fileServerMW(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	recJS := httptest.NewRecorder()
	h502JS.ServeHTTP(recJS, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if recJS.Code != http.StatusOK || !strings.Contains(recJS.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("expected JavaScript Content-Type after 502 fallback, got code=%d content-type=%q", recJS.Code, recJS.Header().Get("Content-Type"))
	}
}
