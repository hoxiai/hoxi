package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hoxiai/hoxi/internal/buildinfo"
	"github.com/hoxiai/hoxi/internal/runner"
)

// HeadlessBootFunc starts an in-process headless Hoxi gateway rooted at rootDir
// and returns its loopback HTTP base URL (e.g. "http://127.0.0.1:54123") and a
// cleanup function.
type HeadlessBootFunc func(ctx context.Context, rootDir string) (baseURL string, cleanup func(), err error)

// Options configures a CLI invocation.
type Options struct {
	Runtime     string
	ModelID     string
	ProviderID  string
	Mode        string
	SandboxMode string
	SessionID   string
	Directory   string
	BridgeAddr  string
	TUI         bool
}

var knownSubcommands = map[string]bool{
	"run":         true,
	"chat":        true,
	"tui":         true,
	"open":        true,
	"models":      true,
	"handoff":     true,
	"worktree":    true,
	"sessions":    true,
	"status":      true,
	"install-cli": true,
	"help":        true,
	"--help":      true,
	"-h":          true,
}

// IsServeCommand reports whether args invokes `hoxi serve [...]` and rewrites
// it into `--no-desktop` flags for the headless daemon entrypoint.
func IsServeCommand(args []string) ([]string, bool) {
	if len(args) > 1 && strings.TrimSpace(args[1]) == "serve" {
		rewritten := make([]string, 0, len(args))
		rewritten = append(rewritten, args[0], "--no-desktop")
		rewritten = append(rewritten, args[2:]...)
		return rewritten, true
	}
	return args, false
}

// ShouldHandle decides whether the current process invocation should run the
// terminal CLI rather than launching the Wails desktop GUI.
func ShouldHandle(args []string, getenv func(string) string) bool {
	if len(args) == 0 {
		return false
	}
	if getenv != nil {
		if strings.TrimSpace(getenv("devserver")) != "" || strings.TrimSpace(getenv("HOXI_DESKTOP")) == "1" {
			return false
		}
		if strings.TrimSpace(getenv("HOXI_NO_DESKTOP")) == "1" {
			return false
		}
	}
	for _, a := range args[1:] {
		if a == "--no-desktop" || a == "-no-desktop" || a == "--version" || a == "-version" {
			return false
		}
		// macOS LaunchServices passes -psn_0_... when launching .app bundles.
		if strings.HasPrefix(a, "-psn_") {
			return false
		}
	}

	if len(args) > 1 {
		first := strings.TrimSpace(args[1])
		if knownSubcommands[first] {
			return true
		}
		// Any positional prompt or CLI-specific flag enters CLI mode.
		if !strings.HasPrefix(first, "-") {
			return true
		}
		for _, a := range args[1:] {
			switch a {
			case "-r", "--runtime", "-m", "--model", "-w", "--worktree", "-s", "--session", "-p", "--prompt", "--tui":
				return true
			}
		}
	}

	// Bare invocation (`hoxi` with no arguments):
	// When launched by macOS Finder/Dock from inside Hoxi.app/Contents/MacOS/Hoxi,
	// start the Wails desktop GUI. When invoked as `hoxi` in a terminal (e.g., via
	// /usr/local/bin/hoxi symlink or `cmd/hoxi`), enter the interactive terminal CLI.
	exeArg := filepath.ToSlash(args[0])
	if strings.Contains(exeArg, ".app/Contents/MacOS/") {
		return false
	}
	base := filepath.Base(exeArg)
	return base == "hoxi" || base == "hoxi.exe"
}

// NormalizeRuntime maps shorthand runtime aliases ("claude", "agy", "oc") to
// canonical Hoxi runtime identifiers.
func NormalizeRuntime(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "opencode", "oc":
		return "opencode"
	case "claude", "claude-code", "claudecode":
		return string(runner.RuntimeClaudeCode)
	case "agy", "antigravity", "gemini":
		return string(runner.RuntimeAntigravity)
	case "qoder", "qodercli":
		return string(runner.RuntimeQoder)
	case "codebuddy", "cb":
		return string(runner.RuntimeCodeBuddy)
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

// Run executes the Hoxi terminal CLI and returns a process exit code.
func Run(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
	defaultBridgeAddr string,
	bootHeadless HeadlessBootFunc,
) int {
	cwd, _ := os.Getwd()
	if len(args) > 1 {
		switch strings.TrimSpace(args[1]) {
		case "help", "-h", "--help":
			printHelp(stdout)
			return 0
		case "open":
			return runOpenCommand(ctx, args[2:], cwd, defaultBridgeAddr, stdout, stderr)
		case "models":
			return runModelsCommand(ctx, args[2:], cwd, defaultBridgeAddr, bootHeadless, stdout, stderr)
		case "sessions":
			return runSessionsCommand(ctx, args[2:], cwd, defaultBridgeAddr, bootHeadless, stdout, stderr)
		case "worktree":
			return runWorktreeCommand(ctx, args[2:], cwd, defaultBridgeAddr, bootHeadless, stdout, stderr)
		case "handoff":
			return runHandoffCommand(ctx, args[2:], cwd, defaultBridgeAddr, bootHeadless, stdout, stderr)
		case "status":
			return runStatusCommand(ctx, cwd, defaultBridgeAddr, stdout, stderr)
		case "install-cli":
			return runInstallCLICommand(args[2:], stdout, stderr)
		case "tui":
			return runNativeOpenCodeTUI(ctx, args[2:], cwd, stdout, stderr)
		}
	}

	subcmd := "chat"
	flagArgs := args[1:]
	if len(args) > 1 {
		first := strings.TrimSpace(args[1])
		if first == "run" || first == "chat" {
			subcmd = first
			flagArgs = args[2:]
		}
	}

	fs := flag.NewFlagSet("hoxi", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		opts       Options
		worktreeOn bool
		promptFlag string
		showHelp   bool
	)
	fs.StringVar(&opts.Runtime, "runtime", "opencode", "agent runtime: opencode | claude | agy | qoder | codebuddy")
	fs.StringVar(&opts.Runtime, "r", "opencode", "shorthand for --runtime")
	fs.StringVar(&opts.ModelID, "model", "", "model ID override")
	fs.StringVar(&opts.ModelID, "m", "", "shorthand for --model")
	fs.StringVar(&opts.ProviderID, "provider", "", "provider ID override")
	fs.StringVar(&opts.Mode, "mode", "build", "agent mode (build | plan | ask)")
	fs.BoolVar(&worktreeOn, "worktree", false, "run session inside an isolated Git worktree (.hoxi/worktrees/<id>)")
	fs.BoolVar(&worktreeOn, "w", false, "shorthand for --worktree")
	fs.StringVar(&opts.SessionID, "session", "", "existing session ID to resume")
	fs.StringVar(&opts.SessionID, "s", "", "shorthand for --session")
	fs.StringVar(&opts.Directory, "dir", cwd, "workspace directory")
	fs.StringVar(&opts.BridgeAddr, "address", defaultBridgeAddr, "Hoxi gateway loopback address")
	fs.StringVar(&promptFlag, "prompt", "", "prompt to execute")
	fs.StringVar(&promptFlag, "p", "", "shorthand for --prompt")
	fs.BoolVar(&opts.TUI, "tui", false, "launch full-screen OpenCode TUI attached to Hoxi")
	fs.BoolVar(&showHelp, "help", false, "show help")
	fs.BoolVar(&showHelp, "h", false, "show help")

	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if showHelp {
		printHelp(stdout)
		return 0
	}

	opts.Runtime = NormalizeRuntime(opts.Runtime)
	if opts.Runtime != "opencode" && !runner.IsValidRuntime(opts.Runtime) {
		fmt.Fprintf(stderr, "Error: unsupported runtime %q (supported: opencode, claude, agy, qoder, codebuddy)\n", opts.Runtime)
		return 2
	}
	opts.SandboxMode = "local"
	if worktreeOn {
		opts.SandboxMode = "worktree"
	}
	if absDir, err := filepath.Abs(opts.Directory); err == nil {
		opts.Directory = absDir
	}

	if opts.TUI {
		return runNativeOpenCodeTUI(ctx, fs.Args(), opts.Directory, stdout, stderr)
	}

	prompt := strings.TrimSpace(promptFlag)
	if prompt == "" && fs.NArg() > 0 {
		prompt = strings.TrimSpace(strings.Join(fs.Args(), " "))
	}

	client, cleanup, err := ConnectOrSpawn(ctx, opts.BridgeAddr, opts.Directory, bootHeadless)
	if err != nil {
		fmt.Fprintf(stderr, "Error starting Hoxi engine: %v\n", err)
		return 1
	}
	defer cleanup()

	if subcmd == "run" || (prompt != "" && subcmd == "chat" && !isInteractiveFlagSet(args)) {
		if prompt == "" {
			fmt.Fprintln(stderr, "Error: prompt text is required for `hoxi run`")
			return 2
		}
		return executeSingleTurn(ctx, client, opts, prompt, stdin, stdout, stderr)
	}

	return RunREPL(ctx, client, opts, prompt, stdin, stdout, stderr)
}

func isInteractiveFlagSet(args []string) bool {
	return len(args) > 1 && strings.TrimSpace(args[1]) == "chat"
}

func printHelp(w io.Writer) {
	fmt.Fprintf(w, `Hoxi CLI (%s) — Multi-Runtime Agentic Engineering Terminal

Usage:
  hoxi [flags]                        Start interactive terminal agent in current directory
  hoxi "<prompt>" [flags]             Execute a prompt directly in the terminal
  hoxi run "<prompt>" [flags]         Run a one-shot task and exit
  hoxi open [directory]               Open or focus the Hoxi desktop workspace window
  hoxi models [-r <runtime>]          List configured models for a runtime
  hoxi sessions                       List sessions in the current directory
  hoxi handoff [list|import]          Inspect or import sessions across Claude/Codex/AGY/OpenCode
  hoxi worktree [list|diff|apply]     Manage isolated Git worktree sandboxes
  hoxi tui                            Launch native OpenCode TUI with Hoxi plugins
  hoxi serve                          Run headless Hoxi daemon for mobile/remote control
  hoxi install-cli                    Install 'hoxi' command into /usr/local/bin or ~/.local/bin

Flags:
  -r, --runtime <name>   Agent runtime: opencode (default) | claude | agy | qoder | codebuddy
  -m, --model <id>       Model ID override (e.g. provider/model or model-id)
  -w, --worktree         Isolate session in a Git worktree (.hoxi/worktrees/<session-id>)
  -s, --session <id>     Resume an existing Hoxi session ID
      --mode <mode>      Agent mode: build (default) | plan
      --dir <path>       Target project directory (defaults to current working directory)
      --tui              Attach full-screen OpenCode TUI

Interactive Slash Commands:
  /runtime <name>        Switch runtime mid-session (opencode, claude, agy, qoder, codebuddy)
  /model [id]            List available models or switch active model
  /worktree [on|off]     Toggle Git worktree sandbox isolation
  /diff                  Show current session's Git diff summary
  /open                  Wake up Hoxi Desktop App focused on the current session
  /handoff               Import an external CLI session into the current conversation
  /sessions              List recent sessions in this workspace
  /new                   Start a fresh session
  /exit                  Exit Hoxi CLI
`, buildinfo.Version)
}
