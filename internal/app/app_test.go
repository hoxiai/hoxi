package app

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"testing"

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

	// Start a dummy long-running sleep process and register via setChild
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start dummy sleep process: %v", err)
	}
	dapp.setChild(cmd)
	if dapp.child != cmd {
		t.Fatal("expected dapp.child to be set")
	}

	// killChild should terminate the process and clear dapp.child
	dapp.killChild()
	if dapp.child != nil {
		t.Fatal("expected dapp.child to be nil after killChild")
	}
	_ = cmd.Wait()

	// Start another dummy process to verify shutdown cleans up both wsServer and child
	cmd2 := exec.Command("sleep", "30")
	if err := cmd2.Start(); err != nil {
		t.Fatalf("failed to start second dummy sleep process: %v", err)
	}
	dapp.setChild(cmd2)

	dapp.startup(context.Background())
	dapp.shutdown(context.Background())
	if dapp.child != nil {
		t.Fatal("expected shutdown to clear child process")
	}
	_ = cmd2.Wait()

	// BootstrapOpenCodeAsync with an immediately cancelled context and failing binary returns cleanly
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dapp.BootstrapOpenCodeAsync(ctx, 0, "testpass")
}

func TestResolveIdentity(t *testing.T) {
	t.Setenv("HOXI_VARIANT", "")

	rel := ResolveIdentity("release", "/Applications/Hoxi.app/Contents/MacOS/Hoxi")
	if rel.Variant != "release" || rel.Title != "Hoxi" || rel.BundleID != "ai.hoxi.desktop" || rel.DefaultBridgeAddr != "127.0.0.1:5088" {
		t.Fatalf("unexpected release identity: %+v", rel)
	}

	devByFlag := ResolveIdentity("dev", "/Applications/Hoxi.app/Contents/MacOS/Hoxi")
	if devByFlag.Variant != "dev" || devByFlag.Title != "Hoxi Dev" || devByFlag.BundleID != "ai.hoxi.desktop.dev" || devByFlag.DefaultBridgeAddr != "127.0.0.1:5089" {
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
