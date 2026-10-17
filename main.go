package main

import (
	"context"
	"crypto/rand"
	"embed"
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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:web/dist
var embeddedAssets embed.FS

type appConfig struct {
	Address   string
	OpenCode  string
	RootDir   string
	NoDesktop bool
}

type desktopApp struct {
	context  context.Context
	cfg      appConfig
	logger   *slog.Logger
	childMu  sync.Mutex
	child    *exec.Cmd
	gateway  *desktopGateway
	status   *desktopStatusTracker
	wsServer *http.Server
	quitting bool
}

func main() {
	enrichSystemPath()

	var cfg appConfig
	cwd, _ := os.Getwd()
	flag.StringVar(&cfg.Address, "address", envOr("HOXI_BRIDGE_ADDR", "127.0.0.1:5088"), "address for the hoxi loopback bridge / headless server")
	flag.StringVar(&cfg.OpenCode, "opencode", envOr("HOXI_OPENCODE", "opencode"), "path to the opencode executable")
	flag.StringVar(&cfg.RootDir, "dir", envOr("HOXI_ROOT_DIR", cwd), "workspace root directory")
	flag.BoolVar(&cfg.NoDesktop, "no-desktop", os.Getenv("HOXI_NO_DESKTOP") == "1", "run in headless HTTP mode without Wails desktop window")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	distFS, err := fs.Sub(embeddedAssets, "web/dist")
	if err != nil {
		logger.Error("failed to mount embedded web/dist", "error", err)
		os.Exit(1)
	}

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

	statusTracker := newDesktopStatusTracker()
	gateway := newDesktopGateway(port, password, cfg.RootDir, distFS, statusTracker)

	app := &desktopApp{
		cfg:     cfg,
		logger:  logger,
		gateway: gateway,
		status:  statusTracker,
	}
	defer app.killChild()

	// Fast path: if opencode binary is already present locally (or bundled in Hoxi.app), start it immediately.
	// Slow path: if opencode is missing on the user's machine, launch the UI window without crashing and
	// auto-download official opencode into ~/.hoxi/bin in the background.
	if localBin := findOpenCodeBinary(cfg.OpenCode); localBin != "" {
		child, startErr := startOpenCodeDaemonWithBin(ctx, localBin, cfg.RootDir, port, password, logger)
		if startErr != nil {
			logger.Warn("initial opencode start failed; will retry in background", "error", startErr)
			statusTracker.setAvailable(false)
			go app.BootstrapOpenCodeAsync(ctx, port, password)
		} else {
			app.setChild(child)
			statusTracker.setAvailable(true)
		}
	} else if cfg.NoDesktop {
		binPath, dlErr := resolveOrInstallOpenCode(ctx, cfg.OpenCode, logger)
		if dlErr != nil {
			logger.Error("failed to resolve or install opencode", "error", dlErr)
			os.Exit(1)
		}
		child, startErr := startOpenCodeDaemonWithBin(ctx, binPath, cfg.RootDir, port, password, logger)
		if startErr != nil {
			logger.Error("failed to start opencode daemon", "error", startErr)
			os.Exit(1)
		}
		app.setChild(child)
	} else {
		statusTracker.setAvailable(false)
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
	gateway.setWSBaseURL("ws://" + loopbackAddr)

	wsServer := &http.Server{
		Handler:           gateway,
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

	gateway.onOpenDirectoryDialog = func() (string, error) {
		if app.context == nil {
			return "", fmt.Errorf("desktop window context not ready")
		}
		return wailsruntime.OpenDirectoryDialog(app.context, wailsruntime.OpenDialogOptions{
			Title: "Select Workspace Project Folder",
		})
	}

	err = wails.Run(&options.App{
		Title:                    "Hoxi",
		Width:                    1200,
		Height:                   760,
		MinWidth:                 880,
		MinHeight:                560,
		HideWindowOnClose:        runtime.GOOS == "darwin",
		EnableDefaultContextMenu: true,
		BackgroundColour:         options.NewRGBA(0, 0, 0, 0),
		AssetServer: &assetserver.Options{
			Assets:  distFS,
			Handler: gateway,
		},
		Mac: &mac.Options{
			TitleBar:             mac.TitleBarHiddenInset(),
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
			About: &mac.AboutInfo{
				Title:   "Hoxi",
				Message: "Native AI Agentic Engineering Workspace (https://hoxi.ai)",
			},
		},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "ai.hoxi.desktop",
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

func (a *desktopApp) setChild(c *exec.Cmd) {
	a.childMu.Lock()
	defer a.childMu.Unlock()
	a.child = c
}

func (a *desktopApp) killChild() {
	a.childMu.Lock()
	defer a.childMu.Unlock()
	if a.child != nil && a.child.Process != nil {
		_ = a.child.Process.Kill()
		a.child = nil
	}
}

func (a *desktopApp) BootstrapOpenCodeAsync(ctx context.Context, port int, password string) {
	binPath, err := resolveOrInstallOpenCode(ctx, a.cfg.OpenCode, a.logger)
	if err != nil {
		a.logger.Error("background opencode resolution failed", "error", err)
		return
	}
	child, err := startOpenCodeDaemonWithBin(ctx, binPath, a.cfg.RootDir, port, password, a.logger)
	if err != nil {
		a.logger.Error("background opencode daemon start failed", "error", err)
		return
	}
	a.setChild(child)
	a.status.setAvailable(true)
}

func (a *desktopApp) startup(ctx context.Context) {
	a.context = ctx
	startDesktopStatusBar(a)
	updateDesktopStatusBar(a.status.snapshot())
}

func (a *desktopApp) shutdown(_ context.Context) {
	stopDesktopStatusBar()
	if a.wsServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.wsServer.Shutdown(ctx)
	}
	a.killChild()
}

func (a *desktopApp) onSecondInstance(_ options.SecondInstanceData) {
	if a.context != nil {
		wailsruntime.WindowShow(a.context)
		wailsruntime.WindowUnminimise(a.context)
	}
}

func startOpenCodeDaemonWithBin(
	ctx context.Context,
	binPath, rootDir string,
	port int,
	password string,
	logger *slog.Logger,
) (*exec.Cmd, error) {
	hoxiConfigPath := ensureHoxiModelsConfig(rootDir)

	args := []string{"serve", "--hostname", "127.0.0.1", "--port", strconv.Itoa(port)}
	child := exec.CommandContext(ctx, binPath, args...)
	child.Dir = rootDir
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	child.Env = append(os.Environ(),
		"OPENCODE_SERVER_HOSTNAME=127.0.0.1",
		"OPENCODE_SERVER_PORT="+strconv.Itoa(port),
		"OPENCODE_SERVER_USERNAME=opencode",
		"OPENCODE_SERVER_PASSWORD="+password,
		"OPENCODE_DISABLE_CONFIG_DEPENDENCY_INSTALL=1",
		"NO_PROXY=127.0.0.1,localhost",
	)
	if hoxiConfigPath != "" {
		child.Env = append(child.Env, "OPENCODE_CONFIG="+hoxiConfigPath)
	}
	if err := child.Start(); err != nil {
		return nil, fmt.Errorf("start opencode serve (%s): %w", binPath, err)
	}
	logger.Info("started opencode daemon", "bin", binPath, "pid", child.Process.Pid, "port", port)

	if err := waitForOpenCodeHealth(ctx, port, password, 15*time.Second); err != nil {
		_ = child.Process.Kill()
		return nil, err
	}
	return child, nil
}

func waitForOpenCodeHealth(ctx context.Context, port int, password string, timeout time.Duration) error {
	client := &http.Client{
		Timeout:   1500 * time.Millisecond,
		Transport: &http.Transport{Proxy: nil},
	}
	healthURL := "http://127.0.0.1:" + strconv.Itoa(port) + "/global/health"
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
		req.SetBasicAuth("opencode", password)
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("opencode health check timed out on port %d after %s", port, timeout)
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
