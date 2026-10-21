package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
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

	"github.com/hoxiai/hoxi/internal/agyhook"
	"github.com/hoxiai/hoxi/internal/buildinfo"
	"github.com/hoxiai/hoxi/internal/cli"
	"github.com/hoxiai/hoxi/internal/diagnostics"
	"github.com/hoxiai/hoxi/internal/gateway"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/opencode"
	"github.com/hoxiai/hoxi/internal/statusbar"
	"github.com/hoxiai/hoxi/internal/telemetry"
	"github.com/hoxiai/hoxi/internal/updater"
	"github.com/hoxiai/hoxi/internal/workspace"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

var (
	// BuildVariant defaults to "dev" so `wails dev` and `go run` automatically run as Hoxi Dev
	// without colliding with an installed production `/Applications/Hoxi.app`.
	// Production builds override this via -ldflags "-X github.com/hoxiai/hoxi/internal/app.BuildVariant=release".
	BuildVariant = "dev"
	appIconBytes []byte
)

func SetAppIcon(icon []byte) {
	appIconBytes = icon
}

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

type Config struct {
	Address   string
	OpenCode  string
	RootDir   string
	NoDesktop bool
}

type DesktopApp struct {
	contextMu       sync.RWMutex
	context         context.Context
	revealOnStartup bool
	cfg             Config
	logger          *slog.Logger
	childMu         sync.Mutex
	child           *exec.Cmd
	childDone       chan struct{}
	childStopped    bool
	engineMu        sync.Mutex
	runCtx          context.Context
	openCodePort    int
	openCodePass    string
	identity        AppIdentity
	respawn         respawner
	respawns        []time.Time
	gateway         *gateway.Gateway
	status          *statusbar.Tracker
	wsServer        *http.Server
	browserEnv      []string
}

func Run(distFS fs.FS) {
	// agy runs `hoxi agy-hook` as a short-lived PreToolUse hook; never start the app for it.
	if len(os.Args) > 1 && os.Args[1] == agyhook.Subcommand {
		os.Exit(agyhook.Run(os.Stdin, os.Stdout, os.Getenv))
	}
	opencode.EnrichSystemPath()

	exePath, _ := os.Executable()
	identity := ResolveIdentity(BuildVariant, exePath)
	if rewritten, ok := cli.IsServeCommand(os.Args); ok {
		os.Args = rewritten
	}
	if cli.ShouldHandle(os.Args, os.Getenv) {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		os.Exit(cli.Run(ctx, os.Args, os.Stdin, os.Stdout, os.Stderr, envOr("HOXI_BRIDGE_ADDR", identity.DefaultBridgeAddr), StartEphemeralHeadless))
	}

	var cfg Config
	var showVersion bool
	cwd, _ := os.Getwd()
	flag.StringVar(&cfg.Address, "address", envOr("HOXI_BRIDGE_ADDR", identity.DefaultBridgeAddr), "address for the hoxi loopback bridge / headless server")
	flag.StringVar(&cfg.OpenCode, "opencode", envOr("HOXI_OPENCODE", "opencode"), "path to the opencode executable")
	flag.StringVar(&cfg.RootDir, "dir", envOr("HOXI_ROOT_DIR", cwd), "workspace root directory")
	flag.BoolVar(&cfg.NoDesktop, "no-desktop", os.Getenv("HOXI_NO_DESKTOP") == "1", "run in headless HTTP mode without Wails desktop window")
	flag.BoolVar(&showVersion, "version", false, "print the embedded Hoxi version and exit")
	flag.Parse()
	if showVersion {
		fmt.Println(buildinfo.Version)
		return
	}

	cfg.RootDir = workspace.ResolveInitialRootDir(cfg.RootDir)

	logErr := diagnostics.Initialize()
	logger := slog.New(diagnostics.NewHandler(os.Stderr))
	slog.SetDefault(logger)
	if logErr != nil {
		logger.Warn("diagnostics storage unavailable")
	}
	diagnostics.Record(diagnostics.Entry{Kind: "system", Level: "INFO", Source: "app", Message: "application starting"})
	defer diagnostics.Record(diagnostics.Entry{Kind: "system", Level: "INFO", Source: "app", Message: "application stopped"})

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
	gw.EnableAgyApprovalHook()
	defer gw.CloseBrowser()

	app := &DesktopApp{
		cfg:          cfg,
		identity:     identity,
		logger:       logger,
		gateway:      gw,
		status:       statusTracker,
		runCtx:       ctx,
		openCodePort: port,
		openCodePass: password,
	}
	defer app.killChild()
	gw.SetEngineInstaller(app.EnsureEngine)

	// The plugin must know the actual fallback port before OpenCode starts.
	host, _, addrErr := net.SplitHostPort(cfg.Address)
	if addrErr != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		logger.Error("bridge address must use a loopback IP", "address", cfg.Address)
		return
	}
	listener, err := net.Listen("tcp", cfg.Address)
	if err != nil {
		logger.Warn("failed to bind preferred loopback address, falling back to random port", "addr", cfg.Address, "error", err)
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			logger.Error("failed to bind loopback listener", "error", err)
			return
		}
	}
	defer listener.Close()
	loopbackAddr := listener.Addr().String()
	gw.SetWSBaseURL("ws://" + loopbackAddr)
	uiOrigins := loopbackUIOrigins(listener.Addr().(*net.TCPAddr))
	if !cfg.NoDesktop {
		// Wails on Windows uses HTTP rather than the native wails:// scheme.
		uiOrigins = append(uiOrigins, "http://wails.localhost")
	}
	if identity.Variant == "dev" {
		uiOrigins = append(uiOrigins, "http://localhost:5421", "http://127.0.0.1:5421", "http://localhost:5411", "http://127.0.0.1:5411")
		// Wails exports its actual development listener (including custom ports).
		if devServer := os.Getenv("devserver"); devServer != "" {
			uiOrigins = append(uiOrigins, "http://"+devServer)
			if _, port, err := net.SplitHostPort(devServer); err == nil {
				uiOrigins = append(uiOrigins, "wails://wails.localhost:"+port)
			}
		}
	} else {
		uiOrigins = append(uiOrigins, "http://localhost:5411", "http://127.0.0.1:5411")
	}
	if origin := os.Getenv("HOXI_BROWSER_UI_ORIGIN"); origin != "" {
		uiOrigins = append(uiOrigins, origin)
	}
	gw.SetBrowserUIOrigins(uiOrigins...)
	gatewayToken, err := generateRandomHex(32)
	if err != nil {
		logger.Error("failed to generate gateway token", "error", err)
		return
	}
	gw.SetGatewayToken(gatewayToken)
	gw.StartRemoteTunnel(hoxipaths.TunnelConfigPath(identity.Variant), listener.Addr().(*net.TCPAddr).Port)
	defer gw.StopRemoteTunnel()
	browserToken, err := generateRandomHex(32)
	if err != nil {
		logger.Error("failed to generate browser credential", "error", err)
		return
	}
	gw.SetBrowserAgentToken(browserToken)
	browserEnv, cleanupPlugin, err := opencode.PrepareBrowserPlugin("http://"+loopbackAddr, browserToken)
	if err != nil {
		logger.Error("failed to prepare browser tools", "error", err)
		return
	}
	defer func() {
		app.killChild()
		cleanupPlugin()
	}()
	app.browserEnv = browserEnv
	wsServer := &http.Server{Handler: gw, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}
	app.wsServer = wsServer
	defer wsServer.Close()
	go func() {
		if err := wsServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			logger.Error("loopback server stopped unexpectedly", "error", err)
		}
	}()

	opencode.ReapOrphanedDaemons(logger)

	// Fast path: if opencode binary is already present locally (or bundled in Hoxi.app), start it immediately.
	// Slow path: if opencode is missing on the user's machine, launch the UI window without crashing and
	// auto-download official opencode into ~/.hoxi/bin in the background.
	if localBin := opencode.FindBinary(cfg.OpenCode); localBin != "" {
		child, startErr := app.startOpenCode(ctx, localBin, port, password)
		if startErr != nil {
			logger.Warn("initial opencode start failed; will retry in background", "error", startErr)
			statusTracker.SetAvailable(false)
			go app.BootstrapOpenCodeAsync(ctx, port, password)
		} else {
			app.setChild(child)
		}
	} else if cfg.NoDesktop {
		binPath, dlErr := opencode.ResolveOrInstall(ctx, cfg.OpenCode, logger)
		if dlErr != nil {
			logger.Error("failed to resolve or install opencode", "error", dlErr)
			os.Exit(1)
		}
		child, startErr := app.startOpenCode(ctx, binPath, port, password)
		if startErr != nil {
			logger.Error("failed to start opencode daemon", "error", startErr)
			os.Exit(1)
		}
		app.setChild(child)
	} else {
		statusTracker.SetAvailable(false)
		go app.BootstrapOpenCodeAsync(ctx, port, password)
	}

	if cfg.NoDesktop {
		logger.Info("hoxi headless server is ready", "url", "http://"+loopbackAddr, "opencodePort", port)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = wsServer.Shutdown(shutdownCtx)
		return
	}

	gw.SetOpenDirectoryDialog(func() (string, error) {
		ctx := app.desktopContext()
		if ctx == nil {
			return "", fmt.Errorf("desktop window context not ready")
		}
		return wailsruntime.OpenDirectoryDialog(ctx, wailsruntime.OpenDialogOptions{
			Title: "Select Workspace Project Folder",
		})
	})
	gw.SetDesktopFocusHandler(func(_, _ string) {
		app.showWindow()
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
			Middleware: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(r.URL.Path, "/api/") {
						gw.ServeHTTP(w, r)
						return
					}
					next.ServeHTTP(w, r)
				})
			},
		},
		Mac: &mac.Options{
			OnUrlOpen:            app.onURLOpen,
			TitleBar:             mac.TitleBarHiddenInset(),
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
			About: &mac.AboutInfo{
				Title:   identity.Title,
				Message: "Native command center for parallel AI engineering.\nMany agents. One workspace.\nhttps://hoxi.ai",
				Icon:    appIconBytes,
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
	if a.childStopped {
		_ = c.Process.Kill()
		_ = c.Wait()
		return
	}
	a.child = c
	if a.status != nil {
		a.status.SetAvailable(true)
	}
	done := make(chan struct{})
	a.childDone = done
	go func() {
		err := c.Wait()
		a.childMu.Lock()
		stopped := a.childStopped
		current := a.child == c
		restart := a.respawn
		if current {
			a.child = nil
			if a.status != nil {
				a.status.SetAvailable(false)
			}
		}
		a.childMu.Unlock()
		if !stopped && a.logger != nil {
			if err != nil {
				exitCode := -1
				if c.ProcessState != nil {
					exitCode = c.ProcessState.ExitCode()
				}
				a.logger.Error("opencode daemon exited unexpectedly", "source", "opencode", "exitCode", exitCode)
			} else {
				a.logger.Warn("opencode daemon exited unexpectedly", "source", "opencode", "exitCode", 0)
			}
		}
		close(done)
		if !stopped && current && restart != nil {
			go a.respawnChild(restart)
		}
	}()
}

func (a *DesktopApp) killChild() {
	a.childMu.Lock()
	a.childStopped = true
	if a.status != nil {
		a.status.SetAvailable(false)
	}
	done := a.childDone
	if a.child != nil && a.child.Process != nil {
		_ = a.child.Process.Kill()
		a.child = nil
	}
	a.childMu.Unlock()
	if done != nil {
		<-done
	}
}

func (a *DesktopApp) BootstrapOpenCodeAsync(ctx context.Context, port int, password string) {
	binPath, err := opencode.ResolveOrInstall(ctx, a.cfg.OpenCode, a.logger)
	if err != nil {
		a.logger.Error("background opencode resolution failed", "error", err)
		return
	}
	child, err := a.startOpenCode(ctx, binPath, port, password)
	if err != nil {
		a.logger.Error("background opencode daemon start failed", "error", err)
		return
	}
	a.setChild(child)
}

func (a *DesktopApp) startup(ctx context.Context) {
	a.contextMu.Lock()
	a.context = ctx
	reveal := a.revealOnStartup
	a.revealOnStartup = false
	a.contextMu.Unlock()
	statusbar.Start(ctx)
	statusbar.Update(a.status.Snapshot())
	installNativeAbout(a.identity.Title, buildinfo.Version)
	telemetry.ReportLaunch(a.logger)
	if BuildVariant == "release" {
		// Wails startup can precede the Cocoa run loop; never wait for it here.
		go func() {
			if err := updater.StartNative(func(force bool) bool {
				if force {
					return a.gateway.BeginUpdateForced()
				}
				return a.gateway.BeginUpdate()
			}, a.gateway.ReleaseUpdate); err != nil {
				a.logger.Info("native updates unavailable", "error", err)
			}
		}()
	}
	if reveal {
		a.showWindow()
	}
}

func (a *DesktopApp) shutdown(_ context.Context) {
	statusbar.Stop()
	if a.wsServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.wsServer.Shutdown(ctx)
	}
	a.killChild()
	if a.gateway != nil {
		a.gateway.StopRemoteTunnel()
		a.gateway.CloseBrowser()
	}
	updater.StopNative()
}

func (a *DesktopApp) onSecondInstance(_ options.SecondInstanceData) {
	if ctx := a.desktopContext(); ctx != nil {
		wailsruntime.WindowShow(ctx)
		wailsruntime.WindowUnminimise(ctx)
	}
}

func (a *DesktopApp) onURLOpen(rawURL string) {
	a.showWindow()
	// URL events can arrive before OnStartup; the gateway already owns the live auth attempt.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := a.gateway.HandleAuthCallback(ctx, rawURL); err != nil {
			// Callback URLs and transport errors can contain credentials. Never log them.
			a.logger.Warn("desktop authorization callback was not completed; polling remains available")
		}
	}()
}

func (a *DesktopApp) showWindow() {
	a.contextMu.Lock()
	ctx := a.context
	if ctx == nil {
		a.revealOnStartup = true
	}
	a.contextMu.Unlock()
	if ctx != nil {
		wailsruntime.WindowShow(ctx)
		wailsruntime.WindowUnminimise(ctx)
	}
}

func (a *DesktopApp) desktopContext() context.Context {
	a.contextMu.RLock()
	defer a.contextMu.RUnlock()
	return a.context
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
	browserEnv, cleanupPlugin, _ := opencode.PrepareBrowserPlugin("http://"+loopbackAddr, browserToken)

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
	}
	gw.SetEngineInstaller(dapp.EnsureEngine)

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
