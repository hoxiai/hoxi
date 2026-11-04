package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/gateway"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/notify"
	"github.com/hoxiai/hoxi/internal/opencode"
	"github.com/hoxiai/hoxi/internal/statusbar"
	"github.com/hoxiai/hoxi/internal/workspace"
)

type AppIdentity struct {
	Variant           string
	Title             string
	BundleID          string
	DefaultBridgeAddr string
}

func ResolveIdentity(variant, exePath string) AppIdentity {
	v := strings.ToLower(strings.TrimSpace(variant))
	if envV := strings.ToLower(strings.TrimSpace(os.Getenv("HOXI_VARIANT"))); envV != "" {
		v = envV
	} else if strings.Contains(exePath, "Hoxi Dev.app") || strings.Contains(exePath, "Hoxi-Dev.app") {
		v = "dev"
	} else if v == "" {
		v = "dev"
	}
	if v == "release" {
		return AppIdentity{
			Variant:           "release",
			Title:             "Hoxi",
			BundleID:          "ai.hoxi.desktop",
			DefaultBridgeAddr: hoxipaths.DefaultReleaseBridgeAddr,
		}
	}
	return AppIdentity{
		Variant:           "dev",
		Title:             "Hoxi Dev",
		BundleID:          "ai.hoxi.desktop.dev",
		DefaultBridgeAddr: hoxipaths.DefaultDevBridgeAddr,
	}
}

func chooseFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func generateRandomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// StartEphemeralHeadless boots a quiet, in-process Hoxi Gateway on an ephemeral
// loopback port when the CLI is invoked while Hoxi Desktop is not running.
func StartEphemeralHeadless(ctx context.Context, rootDir string) (string, func(), error) {
	rootDir = workspace.ResolveInitialRootDir(rootDir)
	port, err := chooseFreePort()
	if err != nil {
		return "", nil, err
	}
	password, err := generateRandomHex(16)
	if err != nil {
		return "", nil, err
	}
	quietLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	statusTracker := statusbar.NewTracker()
	gw := gateway.New(port, password, rootDir, nil, statusTracker)
	gw.EnableAgyApprovalHook()
	gw.StartEnginePrewarm(ctx)
	// Record, but leave sweeping to the long-lived desktop or daemon process.
	ledger := openReclaimLedger(quietLogger)
	gw.SetBrowserLedger(ledger)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		gw.CloseBrowser()
		return "", nil, err
	}
	loopbackAddr := listener.Addr().String()
	gw.SetWSBaseURL("ws://" + loopbackAddr)

	gatewayToken, _ := generateRandomHex(32)
	gw.SetGatewayToken(gatewayToken)
	browserToken, _ := generateRandomHex(32)
	gw.SetBrowserAgentToken(browserToken)
	gw.SetComputerAgentToken(browserToken)
	browserEnv, cleanupPlugin, _ := opencode.PrepareBrowserPlugin("http://"+loopbackAddr, browserToken, ledger)

	dapp := &DesktopApp{
		cfg: Config{
			Address:   loopbackAddr,
			OpenCode:  envOr("HOXI_OPENCODE", "opencode"),
			RootDir:   rootDir,
			NoDesktop: true,
		},
		logger:       quietLogger,
		gateway:      gw,
		status:       statusTracker,
		browserEnv:   browserEnv,
		runCtx:       ctx,
		openCodePort: port,
		openCodePass: password,
		ledger:       ledger,
	}
	gw.SetEngineInstaller(func(ctx context.Context, restart bool) (string, error) {
		if restart {
			return dapp.RestartEngine(ctx)
		}
		return dapp.EnsureEngine(ctx)
	})

	if localBin := opencode.FindBinary(dapp.cfg.OpenCode); localBin != "" {
		if child, startErr := dapp.startOpenCode(ctx, localBin, port, password); startErr == nil {
			dapp.setChild(child)
		} else {
			go dapp.BootstrapOpenCodeAsync(ctx, port, password)
		}
	} else {
		go dapp.BootstrapOpenCodeAsync(ctx, port, password)
	}

	srv := &http.Server{Handler: gw, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}
	dapp.wsServer = srv
	go func() {
		_ = srv.Serve(listener)
	}()

	cleanup := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		_ = listener.Close()
		dapp.killChild()
		gw.CloseBrowser()
		if cleanupPlugin != nil {
			cleanupPlugin()
		}
	}
	return "http://" + loopbackAddr, cleanup, nil
}

// desktopNotificationPermission converts the notify layer's status into the
// gateway's transport-neutral payload. "unavailable" means the build has no
// native notification transport (headless, no app bundle or non-darwin host).
func desktopNotificationPermission(status notify.PermissionStatus) gateway.DesktopNotificationPermission {
	value := string(status)
	if value == "" {
		value = string(notify.PermissionUnknown)
	}
	return gateway.DesktopNotificationPermission{
		Available: status != notify.PermissionUnavailable,
		Status:    value,
	}
}
