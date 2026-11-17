package termhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hoxiai/hoxi/internal/subproc"
)

// NewDefaultService constructs a Service wired to the platform terminal program.
func NewDefaultService() *Service {
	return NewService(DefaultOpener)
}

// DefaultOpener creates a workspace terminal session backed by the user's terminal program.
func DefaultOpener(cwd, _ string, cols, rows uint16) (*StreamHandle, error) {
	prog, args := resolveTerminalProgram()
	cmd := subproc.Command(prog, args...)
	if dir := strings.TrimSpace(cwd); dir != "" {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			cmd.Dir = dir
		}
	}
	env := os.Environ()
	hasTerm := false
	for i, kv := range env {
		if strings.HasPrefix(kv, "TERM=") {
			env[i] = "TERM=xterm-256color"
			hasTerm = true
			break
		}
	}
	if !hasTerm {
		env = append(env, "TERM=xterm-256color")
	}
	env = append(env, "COLORTERM=truecolor")
	cmd.Env = env

	rw, resizeFn, killFn, err := startPlatformPTY(cmd, cols, rows)
	if err != nil {
		return nil, err
	}
	return &StreamHandle{
		RW:     rw,
		Resize: resizeFn,
		Kill:   killFn,
		Wait:   cmd.Wait,
	}, nil
}

func resolveTerminalProgram() (string, []string) {
	if runtime.GOOS == "windows" {
		for _, name := range []string{"pwsh", "powershell"} {
			if p, err := exec.LookPath(name); err == nil {
				return p, []string{"-NoLogo"}
			}
		}
		if comspec := strings.TrimSpace(os.Getenv("COMSPEC")); comspec != "" {
			return comspec, nil
		}
		return "powershell", nil
	}
	if configured := strings.TrimSpace(os.Getenv("SHELL")); configured != "" {
		if fi, err := os.Stat(configured); err == nil && !fi.IsDir() {
			base := filepath.Base(configured)
			if base == "zsh" || base == "bash" || base == "fish" {
				return configured, []string{"-l"}
			}
			return configured, nil
		}
	}
	for _, name := range []string{"zsh", "bash"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, []string{"-l"}
		}
	}
	return "zsh", nil
}
