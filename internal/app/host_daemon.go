package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/cli"
	"github.com/hoxiai/hoxi/internal/gateway"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/remotehost"
	"github.com/hoxiai/hoxi/internal/statusbar"
	"github.com/hoxiai/hoxi/internal/workspace"
)

// RunHostDaemon starts a cross-platform headless Hoxi development host (`hoxi host start`)
// serving authenticated workspace, session, worktree, terminal, and native agent APIs.
func RunHostDaemon(ctx context.Context, opts cli.HostStartOptions, stdout, stderr io.Writer) int {
	rootDir := workspace.ResolveInitialRootDir(opts.RootDir)
	cfg, err := remotehost.EnsureDaemonConfig(
		hoxipaths.HostDaemonPath(),
		opts.Name,
		opts.ListenAddr,
		rootDir,
		opts.RotateToken,
	)
	if err != nil {
		fmt.Fprintf(stderr, "Error initializing host config: %v\n", err)
		return 1
	}

	applyDefaultMemoryLimit()
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	password, err := generateRandomHex(16)
	if err != nil {
		fmt.Fprintf(stderr, "Error generating gateway credential: %v\n", err)
		return 1
	}

	statusTracker := statusbar.NewTracker()
	statusTracker.SetAvailable(true)
	gw := gateway.New(0, password, cfg.RootDir, nil, statusTracker)
	gw.EnableAgyApprovalHook()
	gw.StartEnginePrewarm(ctx)
	gw.StartStorageMaintenance(ctx)
	gw.StartLegacySessionImport()
	gw.StartIdleMemoryRelease(ctx)
	ledger := openReclaimLedger(logger)
	gw.SetBrowserLedger(ledger)
	defer gw.CloseBrowser()

	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		fmt.Fprintf(stderr, "Error listening on %s: %v\n", cfg.ListenAddr, err)
		return 1
	}
	defer listener.Close()

	actualAddr := listener.Addr().String()
	gw.SetWSBaseURL("ws://" + actualAddr)
	gatewayToken, _ := generateRandomHex(32)
	gw.SetGatewayToken(gatewayToken)
	browserToken, _ := generateRandomHex(32)
	gw.SetBrowserAgentToken(browserToken)
	gw.SetComputerAgentToken(browserToken)
	gw.ResumeAfterRestart(ctx)

	hostInfo := remotehost.BuildHostInfo(cfg.HostID, cfg.Name, cfg.RootDir)
	handler := remotehost.WrapBearerGuardWithGatewayToken(cfg.Token, gatewayToken, hostInfo, gw)
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	displayHost := actualAddr
	if h, p, sErr := net.SplitHostPort(actualAddr); sErr == nil && (h == "0.0.0.0" || h == "::") {
		displayHost = net.JoinHostPort(detectPrimaryLANIP(), p)
	}
	publicURL := strings.TrimSpace(opts.PublicURL)
	if publicURL == "" {
		publicURL = "http://" + displayHost
	}
	pairURI := remotehost.FormatPairURI(publicURL, cfg.Name, cfg.Token)

	fmt.Fprintf(stdout, "Hoxi Headless Dev Host (%s) ready\n", hostInfo.Version)
	fmt.Fprintf(stdout, "  Host Name : %s (%s/%s)\n", hostInfo.Name, hostInfo.Platform, hostInfo.Arch)
	fmt.Fprintf(stdout, "  Listen    : http://%s\n", actualAddr)
	fmt.Fprintf(stdout, "  Workspace : %s\n", cfg.RootDir)
	fmt.Fprintf(stdout, "  Pair URI  : %s\n", pairURI)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(listener)
	}()

	select {
	case serveErr := <-errCh:
		if serveErr != nil && serveErr != http.ErrServerClosed {
			fmt.Fprintf(stderr, "Host server stopped with error: %v\n", serveErr)
			return 1
		}
		return 0
	case <-ctx.Done():
		prepCtx, prepCancel := context.WithTimeout(context.Background(), 10*time.Second)
		gw.PrepareForRestart(prepCtx)
		prepCancel()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutCancel()
		_ = srv.Shutdown(shutCtx)
		return 0
	}
}

func detectPrimaryLANIP() string {
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, aErr := iface.Addrs()
			if aErr != nil {
				continue
			}
			for _, addr := range addrs {
				var ip net.IP
				switch v := addr.(type) {
				case *net.IPNet:
					ip = v.IP
				case *net.IPAddr:
					ip = v.IP
				}
				if ip4 := ip.To4(); ip4 != nil && !ip4.IsLoopback() {
					return ip4.String()
				}
			}
		}
	}
	return "127.0.0.1"
}
