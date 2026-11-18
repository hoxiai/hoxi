package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/subproc"
)

type bashTool struct{}

func (t *bashTool) Name() string { return "bash" }
func (t *bashTool) Description() string {
	return "Execute a shell command in the workspace directory with bounded output and timeout, or manage long-lived session-owned background commands (such as dev servers) via run_in_background and action=output|stop|list."
}
func (t *bashTool) IsReadOnly() bool { return false }
func (t *bashTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command":           map[string]any{"type": "string", "description": "Shell command to execute (required when starting a foreground or background command)"},
			"timeoutMs":         map[string]any{"type": "integer", "description": "Timeout in milliseconds for foreground commands (default 120000)"},
			"run_in_background": map[string]any{"type": "boolean", "description": "Set true ONLY for long-lived dev servers or watchers so the call returns a procId immediately while keeping the server alive across steps/turns"},
			"action":            map[string]any{"type": "string", "enum": []string{"run", "output", "stop", "list"}, "description": "Background lifecycle action: 'output' reads unread logs for procId, 'stop' terminates procId, 'list' lists background commands in this session"},
			"procId":            map[string]any{"type": "string", "description": "Background process ID returned by run_in_background (required for action='output' or action='stop')"},
			"waitMs":            map[string]any{"type": "integer", "description": "Optional wait up to 10000ms for new output or exit when action='output'"},
		},
	}
}

func (t *bashTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	var args struct {
		Command         string `json:"command"`
		TimeoutMs       int    `json:"timeoutMs"`
		RunInBackground bool   `json:"run_in_background"`
		Action          string `json:"action"`
		ProcID          string `json:"procId"`
		WaitMs          int    `json:"waitMs"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolOutput{Title: "bash", IsError: true}, err
	}
	act := strings.ToLower(strings.TrimSpace(args.Action))
	if act != "" && act != "run" {
		bgMgr := tc.BackgroundCommands
		if bgMgr == nil {
			bgMgr = fallbackBackgroundManagerForSession(tc.SessionID)
		}
		procID := strings.TrimSpace(args.ProcID)
		switch act {
		case "output":
			wait := time.Duration(0)
			if args.WaitMs > 0 {
				wait = min(maxBgPollWait, time.Duration(args.WaitMs)*time.Millisecond)
			}
			st, err := bgMgr.Output(ctx, procID, wait)
			if err != nil {
				return ToolOutput{Title: "bash output " + procID, IsError: true}, err
			}
			return ToolOutput{
				Title:    "bash output " + st.ProcID,
				Output:   formatBackgroundStatusReport("output", st),
				Metadata: map[string]any{"procId": st.ProcID, "running": st.Running, "exitCode": st.ExitCode},
			}, nil
		case "stop":
			st, err := bgMgr.Stop(ctx, procID)
			if err != nil {
				return ToolOutput{Title: "bash stop " + procID, IsError: true}, err
			}
			return ToolOutput{
				Title:    "bash stop " + st.ProcID,
				Output:   formatBackgroundStatusReport("stopped", st),
				Metadata: map[string]any{"procId": st.ProcID, "running": st.Running, "exitCode": st.ExitCode},
			}, nil
		case "list":
			items, err := bgMgr.List(ctx)
			if err != nil {
				return ToolOutput{Title: "bash list", IsError: true}, err
			}
			return ToolOutput{
				Title:  "bash background list",
				Output: formatBackgroundListReport(items),
			}, nil
		default:
			return ToolOutput{Title: "bash", IsError: true}, fmt.Errorf("unsupported bash action %q; expected run, output, stop, or list", args.Action)
		}
	}

	cmdStr := strings.TrimSpace(args.Command)
	if cmdStr == "" {
		return ToolOutput{Title: "bash", IsError: true}, fmt.Errorf("command is required")
	}
	if args.RunInBackground {
		bgMgr := tc.BackgroundCommands
		if bgMgr == nil {
			bgMgr = fallbackBackgroundManagerForSession(tc.SessionID)
		}
		st, err := bgMgr.Start(ctx, tc.WorktreeDir, cmdStr)
		if err != nil {
			return ToolOutput{Title: cmdStr, IsError: true}, err
		}
		if !st.Running && st.ExitCode != 0 {
			return ToolOutput{
				Title:    cmdStr,
				Output:   formatBackgroundStatusReport("failed", st),
				IsError:  true,
				Metadata: map[string]any{"procId": st.ProcID, "running": false, "exitCode": st.ExitCode},
			}, fmt.Errorf("background command exited with code %d", st.ExitCode)
		}
		return ToolOutput{
			Title:    fmt.Sprintf("%s [%s]", cmdStr, st.ProcID),
			Output:   formatBackgroundStatusReport("started", st),
			Metadata: map[string]any{"procId": st.ProcID, "running": st.Running, "pid": st.PID},
		}, nil
	}
	timeout := 120 * time.Second
	if args.TimeoutMs > 0 && args.TimeoutMs <= 600000 {
		timeout = time.Duration(args.TimeoutMs) * time.Millisecond
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if tc.StreamRunCommand != nil {
		var onLive func(string)
		if tc.OnToolOutput != nil {
			onLive = func(s string) {
				tc.OnToolOutput(stripTerminalControlNoise(s))
			}
		}
		out, code, err := tc.StreamRunCommand(runCtx, tc.WorktreeDir, cmdStr, onLive)
		if err != nil || code != 0 {
			return ToolOutput{
				Title:   cmdStr,
				Output:  fmt.Sprintf("%s\n(exit code %d)", strings.TrimSpace(out), code),
				IsError: true,
			}, fmt.Errorf("exit code %d", code)
		}
		return ToolOutput{Title: cmdStr, Output: out}, nil
	}
	if tc.RunCommand != nil {
		out, code, err := tc.RunCommand(runCtx, tc.WorktreeDir, cmdStr)
		if err != nil || code != 0 {
			return ToolOutput{
				Title:   cmdStr,
				Output:  fmt.Sprintf("%s\n(exit code %d)", strings.TrimSpace(out), code),
				IsError: true,
			}, fmt.Errorf("exit code %d", code)
		}
		return ToolOutput{Title: cmdStr, Output: out}, nil
	}

	shell, shellArgs := defaultShellCommand(cmdStr)
	cmd := subproc.CommandContext(runCtx, shell, shellArgs...)
	cmd.Dir = tc.WorktreeDir
	var combined limitedCmdBuffer
	combined.limit = 128 * 1024
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	err := cmd.Run()
	out := subproc.DecodeOutputBytes(combined.Bytes())
	if err != nil {
		return ToolOutput{
			Title:   cmdStr,
			Output:  strings.TrimSpace(out + "\n" + err.Error()),
			IsError: true,
		}, err
	}
	return ToolOutput{Title: cmdStr, Output: out}, nil
}

type limitedCmdBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *limitedCmdBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 || b.buf.Len() < b.limit {
		avail := len(p)
		if b.limit > 0 && b.buf.Len()+avail > b.limit {
			avail = b.limit - b.buf.Len()
		}
		_, _ = b.buf.Write(p[:avail])
	}
	return len(p), nil
}

func (b *limitedCmdBuffer) Bytes() []byte { return b.buf.Bytes() }

var _ io.Writer = (*limitedCmdBuffer)(nil)

func isWSLStubBashPath(p string) bool {
	norm := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(p), "/", `\`))
	return strings.HasSuffix(norm, `\system32\bash.exe`) ||
		strings.HasSuffix(norm, `\sysnative\bash.exe`) ||
		strings.Contains(norm, `\microsoft\windowsapps\bash.exe`)
}

func resolveWindowsGitBash(getenv func(string) string, fileExists func(string) bool, lookPath func(string) (string, error)) string {
	for _, k := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
		root := strings.TrimSpace(getenv(k))
		if root == "" {
			continue
		}
		for _, rel := range []string{
			root + `\Git\bin\bash.exe`,
			root + `\Git\usr\bin\bash.exe`,
			root + `\Programs\Git\bin\bash.exe`,
		} {
			if fileExists(rel) {
				return rel
			}
		}
	}
	if up := strings.TrimSpace(getenv("USERPROFILE")); up != "" {
		cand := up + `\scoop\apps\git\current\bin\bash.exe`
		if fileExists(cand) {
			return cand
		}
	}
	if lookPath != nil {
		if p, err := lookPath("bash"); err == nil && p != "" && !isWSLStubBashPath(p) {
			return p
		}
	}
	return ""
}

func defaultShellCommand(command string) (string, []string) {
	if runtime.GOOS == "windows" {
		exists := func(p string) bool {
			st, err := os.Stat(filepath.FromSlash(p))
			return err == nil && !st.IsDir()
		}
		if bashBin := resolveWindowsGitBash(os.Getenv, exists, exec.LookPath); bashBin != "" {
			return bashBin, []string{"-c", command}
		}
		return "cmd", []string{"/C", "chcp 65001 >nul 2>&1 & " + command}
	}
	return "/bin/sh", []string{"-c", command}
}
