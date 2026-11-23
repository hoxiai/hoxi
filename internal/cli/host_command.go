package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/remotehost"
)

// HostStartOptions configures `hoxi host start`.
type HostStartOptions struct {
	ListenAddr  string
	RootDir     string
	Name        string
	PublicURL   string
	RotateToken bool
	AllowPublic bool
}

// HostDaemonStartFunc starts a long-lived headless Hoxi development host.
type HostDaemonStartFunc func(ctx context.Context, opts HostStartOptions, stdout, stderr io.Writer) int

var (
	hostRunnerMu sync.RWMutex
	hostRunnerFn HostDaemonStartFunc
)

// SetHostDaemonRunner wires the app-layer headless host daemon starter.
func SetHostDaemonRunner(fn HostDaemonStartFunc) {
	hostRunnerMu.Lock()
	hostRunnerFn = fn
	hostRunnerMu.Unlock()
}

func runHostCommand(ctx context.Context, args []string, cwd string, stdout, stderr io.Writer) int {
	sub := "status"
	rest := args
	if len(args) > 0 && !strings.HasPrefix(strings.TrimSpace(args[0]), "-") {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
		rest = args[1:]
	}

	switch sub {
	case "help", "-h", "--help":
		printHostHelp(stdout)
		return 0

	case "start", "serve":
		fs := flag.NewFlagSet("hoxi host start", flag.ContinueOnError)
		fs.SetOutput(stderr)
		var opts HostStartOptions
		fs.StringVar(&opts.ListenAddr, "listen", remotehost.DefaultHostListenAddr, "TCP listen address (default 127.0.0.1:5430; use SSH/Tailscale or a TLS proxy for remote access)")
		fs.StringVar(&opts.RootDir, "dir", cwd, "default workspace directory")
		fs.StringVar(&opts.Name, "name", "", "display name for this development host")
		fs.StringVar(&opts.PublicURL, "public-url", "", "reachable endpoint URL for the printed pair URI")
		fs.BoolVar(&opts.RotateToken, "rotate-token", false, "generate a fresh bearer token before starting")
		fs.BoolVar(&opts.AllowPublic, "allow-public", false, "allow non-private source IPs (only use behind an HTTPS reverse proxy)")
		if _, err := parseFlagsInterspersed(fs, rest); err != nil {
			return 2
		}
		hostRunnerMu.RLock()
		fn := hostRunnerFn
		hostRunnerMu.RUnlock()
		if fn == nil {
			fmt.Fprintln(stderr, "Error: host daemon runner is not initialized in this binary")
			return 1
		}
		return fn(ctx, opts, stdout, stderr)

	case "token":
		fs := flag.NewFlagSet("hoxi host token", flag.ContinueOnError)
		fs.SetOutput(stderr)
		rotate := fs.Bool("rotate", false, "rotate and persist a new host bearer token")
		if _, err := parseFlagsInterspersed(fs, rest); err != nil {
			return 2
		}
		cfg, err := remotehost.EnsureDaemonConfig(hoxipaths.HostDaemonPath(), "", "", cwd, *rotate)
		if err != nil {
			fmt.Fprintf(stderr, "Error loading host token: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, cfg.Token)
		return 0

	case "pair-uri", "pair":
		fs := flag.NewFlagSet("hoxi host pair-uri", flag.ContinueOnError)
		fs.SetOutput(stderr)
		pubURL := fs.String("public-url", "", "reachable endpoint URL (defaults to configured listen port)")
		name := fs.String("name", "", "override host display label")
		rotate := fs.Bool("rotate", false, "rotate token before printing pair URI")
		if _, err := parseFlagsInterspersed(fs, rest); err != nil {
			return 2
		}
		cfg, err := remotehost.EnsureDaemonConfig(hoxipaths.HostDaemonPath(), *name, "", cwd, *rotate)
		if err != nil {
			fmt.Fprintf(stderr, "Error preparing pair URI: %v\n", err)
			return 1
		}
		endpoint := strings.TrimSpace(*pubURL)
		if endpoint == "" {
			endpoint = resolveLocalProbeEndpoint(cfg.ListenAddr)
		}
		fmt.Fprintln(stdout, remotehost.FormatPairURI(endpoint, cfg.Name, cfg.Token))
		return 0

	case "status":
		fs := flag.NewFlagSet("hoxi host status", flag.ContinueOnError)
		fs.SetOutput(stderr)
		targetURL := fs.String("url", "", "override host endpoint URL to probe")
		asJSON := fs.Bool("json", false, "output JSON status")
		if _, err := parseFlagsInterspersed(fs, rest); err != nil {
			return 2
		}
		cfg, err := remotehost.LoadDaemonConfig(hoxipaths.HostDaemonPath())
		if err != nil {
			fmt.Fprintf(stderr, "No host daemon configured yet at %s (run `hoxi host start` first)\n", hoxipaths.HostDaemonPath())
			return 1
		}
		endpoint := strings.TrimRight(strings.TrimSpace(*targetURL), "/")
		if endpoint == "" {
			endpoint = resolveLocalProbeEndpoint(cfg.ListenAddr)
		}
		probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		req, reqErr := http.NewRequestWithContext(probeCtx, http.MethodGet, endpoint+"/api/hoxi/host-info", nil)
		if reqErr != nil {
			if *asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{"running": false, "endpoint": endpoint, "error": reqErr.Error()})
			} else {
				fmt.Fprintf(stderr, "Invalid host endpoint %q: %v\n", endpoint, reqErr)
			}
			return 1
		}
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
		started := time.Now()
		client := &http.Client{
			Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
			Timeout:   4 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return fmt.Errorf("redirects are not allowed on host-info probe")
			},
		}
		resp, doErr := client.Do(req)
		rttMs := time.Since(started).Milliseconds()
		if doErr != nil {
			errMsg := strings.ReplaceAll(doErr.Error(), cfg.Token, "[REDACTED]")
			if *asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{"running": false, "endpoint": endpoint, "error": errMsg})
			} else {
				fmt.Fprintf(stdout, "Host %q (%s): offline (%s)\n", cfg.Name, endpoint, errMsg)
			}
			return 1
		}
		defer resp.Body.Close()
		var info remotehost.HostInfo
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&info)
		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(stderr, "Host probe returned HTTP %d\n", resp.StatusCode)
			return 1
		}
		if *asJSON {
			_ = json.NewEncoder(stdout).Encode(map[string]any{
				"running":   true,
				"endpoint":  endpoint,
				"latencyMs": rttMs,
				"info":      info,
			})
			return 0
		}
		fmt.Fprintf(stdout, "Host %q (%s): online (%dms) — %s/%s v%s [%s]\n",
			info.Name, endpoint, rttMs, info.Platform, info.Arch, info.Version, info.RootDir)
		return 0

	default:
		fmt.Fprintf(stderr, "Unknown `hoxi host` action %q (expected: start | status | token | pair-uri)\n", sub)
		return 2
	}
}

func resolveLocalProbeEndpoint(listenAddr string) string {
	h, p, err := net.SplitHostPort(strings.TrimSpace(listenAddr))
	if err != nil {
		return "http://" + net.JoinHostPort("127.0.0.1", "5430")
	}
	if h == "" || h == "0.0.0.0" || h == "::" {
		h = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(h, p)
}

func printHostHelp(w io.Writer) {
	fmt.Fprintln(w, `Usage:
  hoxi host start [--listen 127.0.0.1:5430] [--dir <path>] [--name <label>] [--public-url <endpoint>] [--rotate-token] [--allow-public]
  hoxi host status [--url <endpoint>] [--json]
  hoxi host token [--rotate]
  hoxi host pair-uri [--public-url <endpoint>] [--name <label>] [--rotate]`)
}
