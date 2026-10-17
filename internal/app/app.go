package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hoxiai/hoxi/internal/gateway"
	"github.com/hoxiai/hoxi/internal/opencode"
	"github.com/hoxiai/hoxi/internal/provider"
	"github.com/hoxiai/hoxi/internal/statusbar"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

var (
	// BuildVariant can be injected at compile time via -ldflags "-X github.com/hoxiai/hoxi/internal/app.BuildVariant=dev".
	BuildVariant = "release"
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
	}
	if v != "dev" && (strings.Contains(exePath, "Hoxi Dev.app") || strings.Contains(exePath, "Hoxi-Dev.app")) {
		v = "dev"
	}
	if v == "dev" {
		return AppIdentity{
			Variant:           "dev",
			Title:             "Hoxi Dev",
			BundleID:          "ai.hoxi.desktop.dev",
			DefaultBridgeAddr: "127.0.0.1:5089",
		}
	}
	return AppIdentity{
		Variant:           "release",
		Title:             "Hoxi",
		BundleID:          "ai.hoxi.desktop",
		DefaultBridgeAddr: "127.0.0.1:5088",
	}
}

type Config struct {
	Address   string
	OpenCode  string
	RootDir   string
	NoDesktop bool
}

type DesktopApp struct {
	context  context.Context
	cfg      Config
	logger   *slog.Logger
	childMu  sync.Mutex
	child    *exec.Cmd
	gateway  *gateway.Gateway
	status   *statusbar.Tracker
	wsServer *http.Server
}

func Run(distFS fs.FS) {
	opencode.EnrichSystemPath()

	exePath, _ := os.Executable()
	identity := ResolveIdentity(BuildVariant, exePath)

	var cfg Config
	cwd, _ := os.Getwd()
	flag.StringVar(&cfg.Address, "address", envOr("HOXI_BRIDGE_ADDR", identity.DefaultBridgeAddr), "address for the hoxi loopback bridge / headless server")
	flag.StringVar(&cfg.OpenCode, "opencode", envOr("HOXI_OPENCODE", "opencode"), "path to the opencode executable")
	flag.StringVar(&cfg.RootDir, "dir", envOr("HOXI_ROOT_DIR", cwd), "workspace root directory")
	flag.BoolVar(&cfg.NoDesktop, "no-desktop", os.Getenv("HOXI_NO_DESKTOP") == "1", "run in headless HTTP mode without Wails desktop window")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	port, err := chooseFreePort()
	if err != nil {
		logger.Error("failed to allocate free port for opencode", "error", err)
		os.Exit(1)
	}
	password, err := generateRandomHex(16)
	if err != nil {
		logger.Error("failed to generate opencode password", "error", err)
		os.Exit(1)
	}

	statusTracker := statusbar.NewTracker()
	gw := gateway.New(port, password, cfg.RootDir, distFS, statusTracker)

	app := &DesktopApp{
		cfg:     cfg,
		logger:  logger,
		gateway: gw,
		status:  statusTracker,
	}
	defer app.killChild()

	opencode.ReapOrphanedDaemons(logger)

	// Fast path: if opencode binary is already present locally (or bundled in Hoxi.app), start it immediately.
	// Slow path: if opencode is missing on the user's machine, launch the UI window without crashing and
	// auto-download official opencode into ~/.hoxi/bin in the background.
	if localBin := opencode.FindBinary(cfg.OpenCode); localBin != "" {
		cfgPath := provider.EnsureHoxiModelsConfig(cfg.RootDir)
		child, startErr := opencode.StartDaemonWithBin(ctx, localBin, cfg.RootDir, cfgPath, port, password, logger)
		if startErr != nil {
			logger.Warn("initial opencode start failed; will retry in background", "error", startErr)
			statusTracker.SetAvailable(false)
			go app.BootstrapOpenCodeAsync(ctx, port, password)
		} else {
			app.setChild(child)
			statusTracker.SetAvailable(true)
		}
	} else if cfg.NoDesktop {
		binPath, dlErr := opencode.ResolveOrInstall(ctx, cfg.OpenCode, logger)
		if dlErr != nil {
			logger.Error("failed to resolve or install opencode", "error", dlErr)
			os.Exit(1)
		}
		cfgPath := provider.EnsureHoxiModelsConfig(cfg.RootDir)
		child, startErr := opencode.StartDaemonWithBin(ctx, binPath, cfg.RootDir, cfgPath, port, password, logger)
		if startErr != nil {
			logger.Error("failed to start opencode daemon", "error", startErr)
			os.Exit(1)
		}
		app.setChild(child)
	} else {
		statusTracker.SetAvailable(false)
		go app.BootstrapOpenCodeAsync(ctx, port, password)
	}

	// Start local loopback HTTP/WS bridge so WebSocket (/pty/{id}/connect) and headless mode work seamlessly
	listener, err := net.Listen("tcp", cfg.Address)
	if err != nil {
		logger.Warn("failed to bind preferred loopback address, falling back to random port", "addr", cfg.Address, "error", err)
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			logger.Error("failed to bind loopback listener", "error", err)
			os.Exit(1)
		}
	}
	defer listener.Close()

	loopbackAddr := listener.Addr().String()
	gw.SetWSBaseURL("ws://" + loopbackAddr)

	wsServer := &http.Server{
		Handler:           gw,
		ReadHeaderTimeout: 10 * time.Second,
	}
	app.wsServer = wsServer
	go func() {
		_ = wsServer.Serve(listener)
	}()

	if cfg.NoDesktop {
		logger.Info("hoxi headless server is ready", "url", "http://"+loopbackAddr, "opencodePort", port)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = wsServer.Shutdown(shutdownCtx)
		return
	}

	gw.SetOpenDirectoryDialog(func() (string, error) {
		if app.context == nil {
			return "", fmt.Errorf("desktop window context not ready")
		}
		return wailsruntime.OpenDirectoryDialog(app.context, wailsruntime.OpenDialogOptions{
			Title: "Select Workspace Project Folder",
		})
	})

	err = wails.Run(&options.App{
		Title:                    identity.Title,
		Width:                    1200,
		Height:                   760,
		MinWidth:                 880,
		MinHeight:                560,
		HideWindowOnClose:        runtime.GOOS == "darwin",
		EnableDefaultContextMenu: true,
		BackgroundColour:         options.NewRGBA(0, 0, 0, 0),
		AssetServer: &assetserver.Options{
			Assets:  distFS,
			Handler: gw,
		},
		Mac: &mac.Options{
			TitleBar:             mac.TitleBarHiddenInset(),
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
			About: &mac.AboutInfo{
				Title:   identity.Title,
				Message: "Native AI Agentic Engineering Workspace (https://hoxi.ai)",
			},
		},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               identity.BundleID,
			OnSecondInstanceLaunch: app.onSecondInstance,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
	})
	if err != nil {
		logger.Error("wails application exited with error", "error", err)
		os.Exit(1)
	}
}

func (a *DesktopApp) setChild(c *exec.Cmd) {
	a.childMu.Lock()
	defer a.childMu.Unlock()
	a.child = c
}

func (a *DesktopApp) killChild() {
	a.childMu.Lock()
	defer a.childMu.Unlock()
	if a.child != nil && a.child.Process != nil {
		_ = a.child.Process.Kill()
		a.child = nil
	}
}

func (a *DesktopApp) BootstrapOpenCodeAsync(ctx context.Context, port int, password string) {
	binPath, err := opencode.ResolveOrInstall(ctx, a.cfg.OpenCode, a.logger)
	if err != nil {
		a.logger.Error("background opencode resolution failed", "error", err)
		return
	}
	cfgPath := provider.EnsureHoxiModelsConfig(a.cfg.RootDir)
	child, err := opencode.StartDaemonWithBin(ctx, binPath, a.cfg.RootDir, cfgPath, port, password, a.logger)
	if err != nil {
		a.logger.Error("background opencode daemon start failed", "error", err)
		return
	}
	a.setChild(child)
	a.status.SetAvailable(true)
}

func (a *DesktopApp) startup(ctx context.Context) {
	a.context = ctx
	statusbar.Start(ctx)
	statusbar.Update(a.status.Snapshot())
}

func (a *DesktopApp) shutdown(_ context.Context) {
	statusbar.Stop()
	if a.wsServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.wsServer.Shutdown(ctx)
	}
	a.killChild()
}

func (a *DesktopApp) onSecondInstance(_ options.SecondInstanceData) {
	if a.context != nil {
		wailsruntime.WindowShow(a.context)
		wailsruntime.WindowUnminimise(a.context)
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
