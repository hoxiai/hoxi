package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/hoxiai/hoxi/internal/buildinfo"
	"github.com/hoxiai/hoxi/internal/handoff"
	"github.com/hoxiai/hoxi/internal/runner"
)

func executeSingleTurn(
	ctx context.Context,
	client *Client,
	opts Options,
	prompt string,
	stdin io.Reader,
	stdout, stderr io.Writer,
) int {
	session, err := resolveOrCreateSession(ctx, client, opts)
	if err != nil {
		fmt.Fprintf(stderr, "Error creating session: %v\n", err)
		return 1
	}

	renderer := NewTurnRenderer(stdin, stdout, stderr, client, session.Directory)
	err = client.StreamTurn(ctx, session, prompt, opts.Mode, opts.ModelID, opts.ProviderID, func(evt runner.SSEEvent) {
		renderer.HandleEvent(ctx, evt)
	})
	renderer.Finish()
	if err != nil {
		fmt.Fprintf(stderr, "%sError: %v%s\n", ansiRed, err, ansiReset)
		return 1
	}
	return 0
}

// RunREPL runs the interactive Hoxi terminal coding agent loop.
func RunREPL(
	ctx context.Context,
	client *Client,
	opts Options,
	initialPrompt string,
	stdin io.Reader,
	stdout, stderr io.Writer,
) int {
	var session *runner.SessionInfo
	if strings.TrimSpace(opts.SessionID) != "" {
		s, err := client.GetSession(ctx, opts.SessionID, opts.Directory)
		if err != nil {
			fmt.Fprintf(stderr, "Error loading session %s: %v\n", opts.SessionID, err)
			return 1
		}
		session = s
		if s.Runtime != "" {
			opts.Runtime = s.Runtime
		}
		if s.SandboxMode != "" {
			opts.SandboxMode = s.SandboxMode
		}
		if s.ModelID != "" && opts.ModelID == "" {
			opts.ModelID = s.ModelID
		}
	}

	printREPLBanner(stdout, client, opts, session)

	ensureSession := func() (*runner.SessionInfo, error) {
		if session != nil {
			return session, nil
		}
		s, err := client.CreateSession(ctx, opts)
		if err != nil {
			return nil, err
		}
		session = s
		return session, nil
	}

	reader := bufio.NewReaderSize(stdin, 64*1024)

	runPrompt := func(promptText string) {
		s, err := ensureSession()
		if err != nil {
			fmt.Fprintf(stderr, "%sError creating session: %v%s\n", ansiRed, err, ansiReset)
			return
		}
		renderer := NewTurnRenderer(reader, stdout, stderr, client, s.Directory)
		err = client.StreamTurn(ctx, s, promptText, opts.Mode, opts.ModelID, opts.ProviderID, func(evt runner.SSEEvent) {
			renderer.HandleEvent(ctx, evt)
		})
		renderer.Finish()
		if err != nil {
			fmt.Fprintf(stderr, "%sError: %v%s\n", ansiRed, err, ansiReset)
		}
		fmt.Fprintln(stdout)
	}

	if strings.TrimSpace(initialPrompt) != "" {
		runPrompt(strings.TrimSpace(initialPrompt))
	}

	for {
		sandboxBadge := opts.SandboxMode
		if session != nil && session.Branch != "" && opts.SandboxMode == "worktree" {
			sandboxBadge = "wt:" + session.Branch
		}
		fmt.Fprintf(stdout, "%s%s[%s|%s]%s %s❯%s ",
			ansiDim,
			opts.Runtime,
			opts.Mode,
			sandboxBadge,
			ansiReset,
			ansiCyan+ansiBold,
			ansiReset,
		)

		rawLine, readErr := reader.ReadString('\n')
		if readErr != nil && len(rawLine) == 0 {
			fmt.Fprintln(stdout)
			return 0
		}
		line := strings.TrimSpace(rawLine)
		if line == "" {
			if readErr != nil {
				fmt.Fprintln(stdout)
				return 0
			}
			continue
		}

		if strings.HasPrefix(line, "/") {
			done := handleSlashCommand(ctx, line, client, &opts, &session, ensureSession, stdout, stderr)
			if done {
				return 0
			}
			continue
		}

		runPrompt(line)
	}
}

func printREPLBanner(w io.Writer, client *Client, opts Options, session *runner.SessionInfo) {
	connMode := "spawned headless engine"
	if client.IsAttached() {
		connMode = "attached to live Hoxi gateway (" + client.BaseURL() + ")"
	}
	fmt.Fprintf(w, "%sHoxi Terminal%s %s(%s)%s — %s%s%s\n",
		ansiBold, ansiReset,
		ansiDim, buildinfo.Version, ansiReset,
		ansiCyan, connMode, ansiReset,
	)
	fmt.Fprintf(w, "  Workspace : %s\n", opts.Directory)
	fmt.Fprintf(w, "  Runtime   : %s (sandbox: %s, mode: %s)\n", opts.Runtime, opts.SandboxMode, opts.Mode)
	if session != nil {
		fmt.Fprintf(w, "  Session   : %s (%s)\n", session.ID, firstNonEmpty(session.Title, "Untitled"))
	}
	fmt.Fprintf(w, "  %sTip: Type /runtime, /worktree, /diff, /handoff, /open (open in Desktop GUI), or /help%s\n\n", ansiDim, ansiReset)
}

func handleSlashCommand(
	ctx context.Context,
	line string,
	client *Client,
	opts *Options,
	session **runner.SessionInfo,
	ensureSession func() (*runner.SessionInfo, error),
	stdout, stderr io.Writer,
) bool {
	fields := strings.Fields(line)
	cmd := strings.ToLower(fields[0])
	arg := ""
	if len(fields) > 1 {
		arg = strings.TrimSpace(strings.Join(fields[1:], " "))
	}

	switch cmd {
	case "/exit", "/quit", "/q":
		return true

	case "/help", "/?":
		fmt.Fprintln(stdout, `Slash Commands:
  /runtime <opencode|claude|agy|qoder|codebuddy>  Switch active agent runtime
  /model <model-id>                               Set active model ID
  /mode <build|plan>                              Switch agent mode
  /worktree [on|off|status|diff|apply]            Manage Git worktree isolation
  /diff                                           Show modified files in current session
  /sessions                                       List recent sessions in this workspace
  /session <session-id>                           Switch to an existing session
  /handoff [source]                               List or import external CLI sessions
  /open                                           Open current session in Hoxi Desktop GUI
  /tui                                            Launch full-screen OpenCode TUI
  /new                                            Start a fresh session
  /exit                                           Exit Hoxi CLI`)

	case "/new", "/clear":
		*session = nil
		opts.SessionID = ""
		fmt.Fprintf(stdout, "%s✓ Started a new session context.%s\n", ansiGreen, ansiReset)

	case "/runtime", "/r":
		if arg == "" {
			fmt.Fprintf(stdout, "Current runtime: %s (available: opencode, claude, agy, qoder, codebuddy)\n", opts.Runtime)
			return false
		}
		rt := NormalizeRuntime(arg)
		if rt != "opencode" && !runner.IsValidRuntime(rt) {
			fmt.Fprintf(stderr, "Unsupported runtime %q (use: opencode, claude, agy, qoder, codebuddy)\n", arg)
			return false
		}
		if *session != nil {
			updated, err := client.SwitchRuntime(ctx, (*session).ID, rt, opts.ModelID, opts.ProviderID)
			if err != nil {
				fmt.Fprintf(stderr, "Failed to switch runtime: %v\n", err)
				return false
			}
			*session = updated
		}
		opts.Runtime = rt
		fmt.Fprintf(stdout, "%s✓ Switched runtime to %s%s\n", ansiGreen, rt, ansiReset)

	case "/model", "/m":
		if arg == "" {
			fmt.Fprintf(stdout, "Current model: %s\n", firstNonEmpty(opts.ModelID, "(default)"))
			return false
		}
		opts.ModelID = arg
		fmt.Fprintf(stdout, "%s✓ Model set to %s%s\n", ansiGreen, arg, ansiReset)

	case "/mode":
		if arg == "" {
			fmt.Fprintf(stdout, "Current mode: %s\n", opts.Mode)
			return false
		}
		opts.Mode = arg
		fmt.Fprintf(stdout, "%s✓ Mode set to %s%s\n", ansiGreen, arg, ansiReset)

	case "/worktree", "/wt":
		switch strings.ToLower(arg) {
		case "on", "worktree":
			opts.SandboxMode = "worktree"
			if *session != nil {
				if updated, err := client.SwitchSandbox(ctx, (*session).ID, "worktree", false); err == nil {
					*session = updated
				} else {
					fmt.Fprintf(stderr, "Worktree switch failed: %v\n", err)
					return false
				}
			}
			fmt.Fprintf(stdout, "%s✓ Git worktree sandbox enabled.%s\n", ansiGreen, ansiReset)
		case "off", "local":
			opts.SandboxMode = "local"
			if *session != nil {
				if updated, err := client.SwitchSandbox(ctx, (*session).ID, "local", false); err == nil {
					*session = updated
				} else {
					fmt.Fprintf(stderr, "Local sandbox switch failed: %v\n", err)
					return false
				}
			}
			fmt.Fprintf(stdout, "%s✓ Switched to local directory mode.%s\n", ansiGreen, ansiReset)
		case "apply":
			if *session == nil {
				fmt.Fprintln(stderr, "No active session to apply.")
				return false
			}
			if err := client.WorktreeApply(ctx, (*session).ID, opts.Directory, "merge"); err != nil {
				fmt.Fprintf(stderr, "Failed to apply worktree: %v\n", err)
			} else {
				fmt.Fprintf(stdout, "%s✓ Applied worktree changes to base branch.%s\n", ansiGreen, ansiReset)
			}
		case "diff":
			printSessionDiff(ctx, client, *session, opts.Directory, stdout, stderr)
		default:
			if *session == nil {
				fmt.Fprintf(stdout, "Sandbox mode: %s (no active session yet)\n", opts.SandboxMode)
				return false
			}
			st, err := client.WorktreeStatus(ctx, (*session).ID, opts.Directory)
			if err != nil {
				fmt.Fprintf(stderr, "Failed to query worktree status: %v\n", err)
				return false
			}
			fmt.Fprintf(stdout, "Session %s | Branch: %s | Path: %s | Changed files: %d (+%d -%d)\n",
				st.SessionID, firstNonEmpty(st.Branch, "local"), st.Directory, st.ChangedFiles, st.Additions, st.Deletions)
		}

	case "/diff":
		printSessionDiff(ctx, client, *session, opts.Directory, stdout, stderr)

	case "/sessions":
		list, err := client.ListSessions(ctx, opts.Directory)
		if err != nil {
			fmt.Fprintf(stderr, "Error listing sessions: %v\n", err)
			return false
		}
		printSessionList(stdout, list, *session)

	case "/session":
		if arg == "" {
			if *session == nil {
				fmt.Fprintln(stdout, "No active session yet.")
			} else {
				fmt.Fprintf(stdout, "Active session: %s (%s)\n", (*session).ID, (*session).Title)
			}
			return false
		}
		s, err := client.GetSession(ctx, arg, opts.Directory)
		if err != nil {
			fmt.Fprintf(stderr, "Error loading session %s: %v\n", arg, err)
			return false
		}
		*session = s
		opts.SessionID = s.ID
		if s.Runtime != "" {
			opts.Runtime = s.Runtime
		}
		if s.SandboxMode != "" {
			opts.SandboxMode = s.SandboxMode
		}
		fmt.Fprintf(stdout, "%s✓ Switched to session %s (%s)%s\n", ansiGreen, s.ID, s.Title, ansiReset)

	case "/handoff":
		res, err := client.ListHandoffSessions(ctx, opts.Directory, arg, true)
		if err != nil {
			fmt.Fprintf(stderr, "Error scanning external CLI sessions: %v\n", err)
			return false
		}
		if len(res.Sessions) == 0 {
			fmt.Fprintln(stdout, "No matching external CLI sessions found for this directory.")
			return false
		}
		top := res.Sessions[0]
		imp, err := client.ImportHandoffSession(ctx, handoff.HandoffImportRequest{
			Source:          top.Source,
			SessionID:       top.ID,
			SourceCWD:       top.CWD,
			TargetDirectory: opts.Directory,
		})
		if err != nil {
			fmt.Fprintf(stderr, "Handoff import failed: %v\n", err)
			return false
		}
		importedID := extractImportedSessionID(imp)
		if importedID != "" {
			if s, err := client.GetSession(ctx, importedID, opts.Directory); err == nil {
				*session = s
				opts.SessionID = s.ID
			}
		}
		fmt.Fprintf(stdout, "%s✓ Imported %s session %q -> %s%s\n", ansiGreen, top.Source, top.Title, importedID, ansiReset)

	case "/open":
		sid := ""
		if *session != nil {
			sid = (*session).ID
		}
		openDesktopWindow(ctx, client, opts.Directory, sid, stdout, stderr)

	case "/tui":
		s, err := ensureSession()
		targetDir := opts.Directory
		if err == nil && s != nil && s.Directory != "" {
			targetDir = s.Directory
		}
		_ = runNativeOpenCodeTUI(ctx, nil, targetDir, stdout, stderr)

	default:
		fmt.Fprintf(stderr, "Unknown command %q (type /help for available commands)\n", cmd)
	}
	return false
}

func resolveOrCreateSession(ctx context.Context, client *Client, opts Options) (*runner.SessionInfo, error) {
	if strings.TrimSpace(opts.SessionID) != "" {
		return client.GetSession(ctx, opts.SessionID, opts.Directory)
	}
	return client.CreateSession(ctx, opts)
}

func printSessionDiff(ctx context.Context, client *Client, session *runner.SessionInfo, directory string, stdout, stderr io.Writer) {
	if session == nil {
		fmt.Fprintln(stderr, "No active session yet.")
		return
	}
	diffs, err := client.SessionDiff(ctx, session.ID, directory)
	if err != nil {
		fmt.Fprintf(stderr, "Error fetching session diff: %v\n", err)
		return
	}
	if len(diffs) == 0 {
		fmt.Fprintln(stdout, "No uncommitted file changes in this session.")
		return
	}
	for _, d := range diffs {
		fmt.Fprintf(stdout, "  %s%-7s%s %s %s(+%d -%d)%s\n",
			ansiCyan, d.Status, ansiReset,
			filepath.Clean(d.File),
			ansiDim, d.Additions, d.Deletions, ansiReset,
		)
	}
}
