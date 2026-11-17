package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/subproc"
)

type bashTool struct{}

func (t *bashTool) Name() string { return "bash" }
func (t *bashTool) Description() string {
	return "Execute a shell command in the workspace directory with bounded output and timeout."
}
func (t *bashTool) IsReadOnly() bool { return false }
func (t *bashTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command":   map[string]any{"type": "string", "description": "Shell command to execute"},
			"timeoutMs": map[string]any{"type": "integer", "description": "Timeout in milliseconds (default 120000)"},
		},
		"required": []string{"command"},
	}
}

func (t *bashTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	var args struct {
		Command   string `json:"command"`
		TimeoutMs int    `json:"timeoutMs"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolOutput{Title: "bash", IsError: true}, err
	}
	cmdStr := strings.TrimSpace(args.Command)
	if cmdStr == "" {
		return ToolOutput{Title: "bash", IsError: true}, fmt.Errorf("command is required")
	}
	timeout := 120 * time.Second
	if args.TimeoutMs > 0 && args.TimeoutMs <= 600000 {
		timeout = time.Duration(args.TimeoutMs) * time.Millisecond
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

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

	shell, flag := "/bin/sh", "-c"
	if runtime.GOOS == "windows" {
		shell, flag = "cmd", "/C"
	}
	cmd := subproc.CommandContext(runCtx, shell, flag, cmdStr)
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
