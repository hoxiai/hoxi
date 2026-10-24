package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/codeindex"
	"github.com/hoxiai/hoxi/internal/handoff"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/opencode"
	"github.com/hoxiai/hoxi/internal/provider"
	"github.com/hoxiai/hoxi/internal/runner"
	"github.com/hoxiai/hoxi/internal/skills"
)

func runOpenCommand(
	ctx context.Context,
	args []string,
	cwd string,
	defaultBridgeAddr string,
	stdout, stderr io.Writer,
) int {
	targetDir := cwd
	sessionID := ""
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		arg := strings.TrimSpace(args[0])
		if strings.HasPrefix(arg, "ses_") {
			sessionID = arg
		} else if abs, err := filepath.Abs(arg); err == nil {
			targetDir = abs
		}
	}

	for _, addr := range []string{defaultBridgeAddr, hoxipaths.DefaultReleaseBridgeAddr, hoxipaths.DefaultDevBridgeAddr} {
		if strings.TrimSpace(addr) == "" {
			continue
		}
		c := NewClient(addr, true)
		if c.Healthy(ctx) {
			openDesktopWindow(ctx, c, targetDir, sessionID, stdout, stderr)
			return 0
		}
	}

	if err := launchMacOSApp(targetDir); err != nil {
		fmt.Fprintf(stderr, "Error launching Hoxi Desktop: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s✓ Launched Hoxi Desktop for %s%s\n", ansiGreen, targetDir, ansiReset)
	return 0
}

func openDesktopWindow(ctx context.Context, client *Client, directory, sessionID string, stdout, stderr io.Writer) {
	if client != nil && client.IsAttached() {
		if err := client.FocusDesktop(ctx, directory, sessionID); err == nil {
			fmt.Fprintf(stdout, "%s✓ Focused Hoxi Desktop window (%s)%s\n", ansiGreen, directory, ansiReset)
			return
		}
	}
	if err := launchMacOSApp(directory); err != nil {
		fmt.Fprintf(stderr, "Unable to open Hoxi Desktop window: %v\n", err)
		return
	}
	fmt.Fprintf(stdout, "%s✓ Opened Hoxi Desktop window (%s)%s\n", ansiGreen, directory, ansiReset)
}

func launchMacOSApp(directory string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("automatic desktop launch is only supported on macOS")
	}
	candidates := []string{
		"/Applications/Hoxi.app",
		"/Applications/Hoxi Dev.app",
	}
	if exe, err := os.Executable(); err == nil {
		if idx := strings.Index(exe, ".app/Contents/MacOS/"); idx > 0 {
			candidates = append([]string{exe[:idx+4]}, candidates...)
		}
	}
	for _, appPath := range candidates {
		if info, err := os.Stat(appPath); err == nil && info.IsDir() {
			cmd := exec.Command("open", "-a", appPath, directory)
			return cmd.Run()
		}
	}
	return exec.Command("open", "-a", "Hoxi", directory).Run()
}

func runSessionsCommand(
	ctx context.Context,
	args []string,
	cwd string,
	defaultBridgeAddr string,
	bootHeadless HeadlessBootFunc,
	stdout, stderr io.Writer,
) int {
	fs := flag.NewFlagSet("hoxi sessions", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var dir string
	fs.StringVar(&dir, "dir", cwd, "workspace directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	client, cleanup, err := ConnectOrSpawn(ctx, defaultBridgeAddr, dir, bootHeadless)
	if err != nil {
		fmt.Fprintf(stderr, "Error connecting to Hoxi: %v\n", err)
		return 1
	}
	defer cleanup()

	list, err := client.ListSessions(ctx, dir)
	if err != nil {
		fmt.Fprintf(stderr, "Error listing sessions: %v\n", err)
		return 1
	}
	printSessionList(stdout, list, nil)
	return 0
}

func printSessionList(w io.Writer, list []runner.SessionInfo, active *runner.SessionInfo) {
	if len(list) == 0 {
		fmt.Fprintln(w, "No sessions found for this directory.")
		return
	}
	for _, s := range list {
		marker := "  "
		if active != nil && active.ID == s.ID {
			marker = "* "
		}
		rt := firstNonEmpty(s.Runtime, "opencode")
		sb := firstNonEmpty(s.SandboxMode, "local")
		if s.Branch != "" && sb == "worktree" {
			sb = "wt:" + s.Branch
		}
		updated := ""
		if s.Time.Updated > 0 {
			updated = time.UnixMilli(s.Time.Updated).Format("01-02 15:04")
		}
		fmt.Fprintf(w, "%s%-26s  %-12s  %-18s  %-11s  %s\n",
			marker, s.ID, rt, sb, updated, firstNonEmpty(s.Title, "Untitled"))
	}
}

func runModelsCommand(
	ctx context.Context,
	args []string,
	cwd string,
	defaultBridgeAddr string,
	bootHeadless HeadlessBootFunc,
	stdout, stderr io.Writer,
) int {
	fs := flag.NewFlagSet("hoxi models", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var rt string
	fs.StringVar(&rt, "runtime", "opencode", "runtime: opencode | claude | agy | qoder | codebuddy")
	fs.StringVar(&rt, "r", "opencode", "shorthand for --runtime")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		rt = fs.Arg(0)
	}
	rt = NormalizeRuntime(rt)
	client, cleanup, err := ConnectOrSpawn(ctx, defaultBridgeAddr, cwd, bootHeadless)
	if err != nil {
		fmt.Fprintf(stderr, "Error connecting to Hoxi: %v\n", err)
		return 1
	}
	defer cleanup()

	defProv, defMod := client.ResolveModel(ctx, cwd, rt, "", "")
	printModelList(ctx, client, cwd, rt, defProv, defMod, stdout, stderr)
	return 0
}

func printModelList(
	ctx context.Context,
	client *Client,
	directory, runtime, activeProv, activeMod string,
	stdout, stderr io.Writer,
) {
	items, _, _, err := client.ListModels(ctx, directory, runtime)
	if err != nil {
		fmt.Fprintf(stderr, "Error listing models: %v\n", err)
		return
	}
	if len(items) == 0 {
		fmt.Fprintf(stdout, "No models found for runtime %s.\n", runtime)
		return
	}
	for _, it := range items {
		marker := "  "
		if it.ModelID == activeMod && (activeProv == "" || it.ProviderID == activeProv) {
			marker = "* "
		}
		key := it.ModelID
		if NormalizeRuntime(runtime) == "opencode" && it.ProviderID != "" {
			key = it.ProviderID + "/" + it.ModelID
		}
		defBadge := ""
		if it.Default {
			defBadge = " (default)"
		}
		fmt.Fprintf(stdout, "%s%-38s  %s%s\n", marker, key, it.Name, defBadge)
	}
}

func runWorktreeCommand(
	ctx context.Context,
	args []string,
	cwd string,
	defaultBridgeAddr string,
	bootHeadless HeadlessBootFunc,
	stdout, stderr io.Writer,
) int {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
		args = args[1:]
	}
	client, cleanup, err := ConnectOrSpawn(ctx, defaultBridgeAddr, cwd, bootHeadless)
	if err != nil {
		fmt.Fprintf(stderr, "Error connecting to Hoxi: %v\n", err)
		return 1
	}
	defer cleanup()

	switch sub {
	case "list", "ls":
		list, err := client.ListSessions(ctx, cwd)
		if err != nil {
			fmt.Fprintf(stderr, "Error listing sessions: %v\n", err)
			return 1
		}
		count := 0
		for _, s := range list {
			if s.SandboxMode == "worktree" || s.Branch != "" {
				count++
				fmt.Fprintf(stdout, "  %-26s  %-22s  %s\n", s.ID, firstNonEmpty(s.Branch, "worktree"), s.Directory)
			}
		}
		if count == 0 {
			fmt.Fprintln(stdout, "No active Git worktree sandboxes in this project.")
		}
		return 0

	case "diff":
		if len(args) == 0 {
			fmt.Fprintln(stderr, "Usage: hoxi worktree diff <session-id>")
			return 2
		}
		printSessionDiff(ctx, client, &runner.SessionInfo{ID: args[0]}, cwd, stdout, stderr)
		return 0

	case "status":
		if len(args) == 0 {
			fmt.Fprintln(stderr, "Usage: hoxi worktree status <session-id>")
			return 2
		}
		st, err := client.WorktreeStatus(ctx, args[0], cwd)
		if err != nil {
			fmt.Fprintf(stderr, "Error querying worktree status: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Session: %s\nBranch : %s\nPath   : %s\nFiles  : %d (+%d -%d)\n",
			st.SessionID, st.Branch, st.Directory, st.ChangedFiles, st.Additions, st.Deletions)
		return 0

	case "apply":
		if len(args) == 0 {
			fmt.Fprintln(stderr, "Usage: hoxi worktree apply <session-id>")
			return 2
		}
		if err := client.WorktreeApply(ctx, args[0], cwd, "merge"); err != nil {
			fmt.Fprintf(stderr, "Error applying worktree: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%s✓ Applied worktree %s to base branch.%s\n", ansiGreen, args[0], ansiReset)
		return 0

	default:
		fmt.Fprintf(stderr, "Unknown worktree subcommand %q (supported: list, status, diff, apply)\n", sub)
		return 2
	}
}

func runHandoffCommand(
	ctx context.Context,
	args []string,
	cwd string,
	defaultBridgeAddr string,
	bootHeadless HeadlessBootFunc,
	stdout, stderr io.Writer,
) int {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
		args = args[1:]
	}

	fs := flag.NewFlagSet("hoxi handoff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		fromSource string
		toRuntime  string
		sessionID  string
		allDirs    bool
	)
	fs.StringVar(&fromSource, "from", "", "source CLI (claude, codex, gemini, opencode, qoder, codebuddy, grok, trae)")
	fs.StringVar(&toRuntime, "to", "opencode", "target runtime in Hoxi")
	fs.StringVar(&sessionID, "id", "", "external session ID to import")
	fs.BoolVar(&allDirs, "all", false, "include sessions from all directories")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if sessionID == "" && fs.NArg() > 0 {
		sessionID = strings.TrimSpace(fs.Arg(0))
	}

	client, cleanup, err := ConnectOrSpawn(ctx, defaultBridgeAddr, cwd, bootHeadless)
	if err != nil {
		fmt.Fprintf(stderr, "Error connecting to Hoxi: %v\n", err)
		return 1
	}
	defer cleanup()

	res, err := client.ListHandoffSessions(ctx, cwd, fromSource, !allDirs)
	if err != nil {
		fmt.Fprintf(stderr, "Error scanning external CLI sessions: %v\n", err)
		return 1
	}

	if sub == "list" && sessionID == "" {
		if len(res.Sessions) == 0 {
			fmt.Fprintln(stdout, "No external CLI sessions found.")
			return 0
		}
		for _, item := range res.Sessions {
			fmt.Fprintf(stdout, "  %-18s  %-28s  %-6d turns  %s\n",
				item.Source, item.ID, item.TurnCount, firstNonEmpty(item.Title, item.CWD))
		}
		return 0
	}

	var target *handoff.ExternalSessionSummary
	for i := range res.Sessions {
		if sessionID == "" || res.Sessions[i].ID == sessionID {
			target = &res.Sessions[i]
			break
		}
	}
	if target == nil {
		fmt.Fprintf(stderr, "No matching external session found to import.\n")
		return 1
	}

	imp, err := client.ImportHandoffSession(ctx, handoff.HandoffImportRequest{
		Source:              target.Source,
		SessionID:           target.ID,
		SourceCWD:           target.CWD,
		TargetDirectory:     cwd,
		PreferredProviderID: NormalizeRuntime(toRuntime),
	})
	if err != nil {
		fmt.Fprintf(stderr, "Error importing session: %v\n", err)
		return 1
	}
	importedID := extractImportedSessionID(imp)
	fmt.Fprintf(stdout, "%s✓ Imported %s session %q -> Hoxi session %s%s\n",
		ansiGreen, target.Source, target.Title, importedID, ansiReset)
	return 0
}

func extractImportedSessionID(res *handoff.HandoffImportResponse) string {
	if res == nil || res.Session == nil {
		return ""
	}
	id, _ := res.Session["id"].(string)
	return strings.TrimSpace(id)
}

func runStatusCommand(ctx context.Context, cwd, defaultBridgeAddr string, stdout, _ io.Writer) int {
	for _, addr := range []string{defaultBridgeAddr, hoxipaths.DefaultReleaseBridgeAddr, hoxipaths.DefaultDevBridgeAddr} {
		if strings.TrimSpace(addr) == "" {
			continue
		}
		c := NewClient(addr, true)
		if c.Healthy(ctx) {
			fmt.Fprintf(stdout, "%s● Hoxi Gateway is running%s at %s (workspace: %s)\n", ansiGreen, ansiReset, c.BaseURL(), cwd)
			return 0
		}
	}
	fmt.Fprintf(stdout, "○ Hoxi Desktop/Daemon is not currently running (CLI will auto-spawn on demand).\n")
	return 0
}

func runInstallCLICommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hoxi install-cli", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var binDir string
	fs.StringVar(&binDir, "bin-dir", "", "target bin directory (defaults to /usr/local/bin or ~/.local/bin)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "Error resolving current executable: %v\n", err)
		return 1
	}
	linkPath, err := InstallSymlink(exePath, binDir)
	if err != nil {
		fmt.Fprintf(stderr, "Error installing hoxi CLI symlink: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s✓ Installed hoxi CLI at %s -> %s%s\n", ansiGreen, linkPath, exePath, ansiReset)
	return 0
}

// InstallSymlink creates or updates a `hoxi` symlink pointing to exePath.
func InstallSymlink(exePath, preferredBinDir string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}
	candidates := []string{}
	if strings.TrimSpace(preferredBinDir) != "" {
		candidates = append(candidates, strings.TrimSpace(preferredBinDir))
	} else {
		candidates = append(candidates, "/usr/local/bin")
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			candidates = append(candidates, filepath.Join(home, ".local", "bin"))
		}
	}

	var lastErr error
	for _, dir := range candidates {
		if err := os.MkdirAll(dir, 0755); err != nil {
			lastErr = err
			continue
		}
		dest := filepath.Join(dir, "hoxi")
		tmp := filepath.Join(dir, ".hoxi-link.tmp")
		_ = os.Remove(tmp)
		if err := os.Symlink(exePath, tmp); err != nil {
			lastErr = err
			continue
		}
		if err := os.Rename(tmp, dest); err != nil {
			_ = os.Remove(tmp)
			lastErr = err
			continue
		}
		return dest, nil
	}
	return "", lastErr
}

func runNativeOpenCodeTUI(ctx context.Context, args []string, dir string, stdout, stderr io.Writer) int {
	binPath := opencode.FindBinary("opencode")
	if binPath == "" {
		fmt.Fprintln(stderr, "Error: opencode binary not found. Run Hoxi Desktop once or install opencode in PATH.")
		return 1
	}
	if _, err := skills.SeedDefaults(); err != nil {
		fmt.Fprintf(stderr, "Warning: failed to seed default skills: %v\n", err)
	}
	cfgPath := provider.EnsureHoxiModelsConfig(dir)
	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	env := append(os.Environ(), "OPENCODE_DISABLE_CONFIG_DEPENDENCY_INSTALL=1")
	if cfgPath != "" {
		env = append(env, "OPENCODE_CONFIG="+cfgPath)
	}
	cmd.Env = env
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(stderr, "Error running OpenCode TUI: %v\n", err)
		return 1
	}
	return 0
}

func runCodeIndexCommand(args []string, cwd string, stdout, stderr io.Writer) int {
	action := "search"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = strings.ToLower(strings.TrimSpace(args[0]))
		args = args[1:]
	}
	fs := flag.NewFlagSet("hoxi code-index", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		dir          string
		query        string
		symbol       string
		relPath      string
		startLine    int
		endLine      int
		contextLines int
		depth        int
		limit        int
		refresh      bool
	)
	fs.StringVar(&dir, "dir", cwd, "workspace directory")
	fs.StringVar(&query, "q", "", "search query")
	fs.StringVar(&query, "query", "", "search query")
	fs.StringVar(&symbol, "symbol", "", "symbol name for context, inspect, callers, callees, or impact")
	fs.StringVar(&relPath, "path", "", "relative file path or prefix")
	fs.IntVar(&startLine, "start", 0, "1-based start line for inspect")
	fs.IntVar(&startLine, "start-line", 0, "1-based start line for inspect")
	fs.IntVar(&endLine, "end", 0, "1-based end line for inspect")
	fs.IntVar(&endLine, "end-line", 0, "1-based end line for inspect")
	fs.IntVar(&contextLines, "context", 5, "context lines for inspect or context")
	fs.IntVar(&depth, "depth", 2, "call graph depth for impact (1-3)")
	fs.IntVar(&limit, "limit", 12, "maximum results")
	fs.BoolVar(&refresh, "refresh", false, "refresh index before searching")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	pos := strings.TrimSpace(strings.Join(fs.Args(), " "))
	svc := codeindex.NewService("")
	writeOut := func(v any) int {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			fmt.Fprintf(stderr, "Error encoding output: %v\n", err)
			return 1
		}
		return 0
	}
	switch action {
	case "search":
		if query == "" {
			query = firstNonEmpty(pos, symbol)
		}
		if refresh {
			_, _ = svc.Refresh(dir)
		}
		res, err := svc.SearchWithPath(dir, query, relPath, limit)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		return writeOut(res)
	case "inspect":
		if symbol == "" && relPath == "" && pos != "" {
			if strings.Contains(pos, "/") || strings.Contains(pos, ".") {
				relPath = pos
			} else {
				symbol = pos
			}
		}
		res, err := svc.Inspect(dir, relPath, symbol, startLine, endLine, contextLines)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		return writeOut(res)
	case "callers":
		if symbol == "" {
			symbol = firstNonEmpty(pos, query)
		}
		res, err := svc.Callers(dir, symbol, relPath, limit)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		return writeOut(res)
	case "callees":
		if symbol == "" {
			symbol = firstNonEmpty(pos, query)
		}
		res, err := svc.Callees(dir, symbol, relPath, limit)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		return writeOut(res)
	case "impact":
		if symbol == "" {
			symbol = firstNonEmpty(pos, query)
		}
		res, err := svc.Impact(dir, symbol, relPath, depth, limit)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		return writeOut(res)
	case "context", "explore":
		if symbol == "" && query == "" && pos != "" {
			symbol = pos
		}
		res, err := svc.Context(dir, relPath, symbol, query, contextLines, limit)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		return writeOut(res)
	case "refresh":
		res, err := svc.Refresh(dir)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		return writeOut(res)
	case "status":
		res, err := svc.Status(dir)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		return writeOut(res)
	default:
		fmt.Fprintf(stderr, "Unknown code-index action %q (supported: context, search, inspect, callers, callees, impact, refresh, status)\n", action)
		return 2
	}
}
