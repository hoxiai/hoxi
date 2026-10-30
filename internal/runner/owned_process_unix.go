//go:build !windows

package runner

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/subproc"
)

// pgrepPIDs lists the processes `pkill args…` would signal right now, by
// running pgrep with the same flags and pattern. ok is false when pgrep
// fails for any reason other than "nothing matched".
func pgrepPIDs(args []string) ([]int, bool) {
	bin := "/usr/bin/pgrep"
	if _, err := os.Stat(bin); err != nil {
		bin = "pgrep"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := subproc.CommandContext(ctx, bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return nil, false
	}
	raw, _ := io.ReadAll(io.LimitReader(stdout, 1<<20))
	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && strings.TrimSpace(string(raw)) == "" {
			return nil, true // no process matched
		}
		return nil, false
	}
	var pids []int
	for _, field := range strings.Fields(string(raw)) {
		pid, err := strconv.Atoi(field)
		if err != nil || pid <= 0 {
			return nil, false
		}
		pids = append(pids, pid)
	}
	return pids, true
}
