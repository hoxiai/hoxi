package agent

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/hoxiai/hoxi/internal/subproc"
)

const (
	maxCommandLogBytes  = 4 << 20
	commandLogPageBytes = 8 << 10
	maxTurnCommandLogs  = 4
)

type commandLogCapture struct {
	mu     sync.Mutex
	data   []byte
	total  int64
	closed bool
}

func (c *commandLogCapture) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.total += int64(len(b))
		c.data = append(c.data, b[:min(len(b), maxCommandLogBytes-len(c.data))]...)
	}
	return len(b), nil
}

type commandLog struct {
	id    string
	data  []byte
	total int64
}

// A new store belongs to each RunTurn, including each child turn.
type turnCommandLogs struct {
	mu   sync.Mutex
	logs []commandLog
}

func (s *turnCommandLogs) finish(c *commandLogCapture, input json.RawMessage, out *ToolOutput) {
	c.mu.Lock()
	c.closed = true
	log := commandLog{data: c.data, total: c.total}
	c.data = nil
	c.mu.Unlock()
	if _, promoted := out.Metadata["procId"]; promoted || log.total <= 4096 {
		return
	}
	preview := compactBashToolOutput(input, out.Output, out.IsError, maxToolOutputBytes-768)
	if log.total <= maxToolOutputBytes && preview == out.Output && log.total <= int64(len(out.Output)) {
		return
	}
	log.id = "log_" + rand.Text()
	s.mu.Lock()
	if len(s.logs) == maxTurnCommandLogs {
		copy(s.logs, s.logs[1:])
		s.logs = s.logs[:len(s.logs)-1]
	}
	s.logs = append(s.logs, log)
	s.mu.Unlock()

	// The runner's own bounded preview may already have discarded the only
	// diagnostic. Look in the captured bytes before returning that preview.
	if log.total > int64(len(out.Output)) {
		if match := failureMarkerRe.FindIndex(log.data); match != nil {
			start := match[0]
			if log.data[start] == '\n' {
				start++
			}
			end := start + min(400, len(log.data)-start)
			if n := bytes.IndexByte(log.data[start:end], '\n'); n >= 0 {
				end = start + n
			}
			line := strings.TrimSpace(subproc.DecodeOutputBytes(log.data[start:end]))
			if line != "" && !strings.Contains(preview, line) {
				preview += fmt.Sprintf("\nDiagnostic at byte %d: %s", start, line)
			}
		}
	}
	out.Output = fmt.Sprintf("%s\n[Foreground log: bash action=output procId=%s offset=0; retained %d/%d bytes for this turn]", preview, log.id, len(log.data), log.total)
	if out.Metadata == nil {
		out.Metadata = make(map[string]any)
	}
	out.Metadata["logId"] = log.id
}

func (s *turnCommandLogs) page(id string, offset int) (ToolOutput, error) {
	if s != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, log := range s.logs {
			if log.id != id {
				continue
			}
			if offset < 0 || offset > len(log.data) {
				return ToolOutput{IsError: true}, fmt.Errorf("offset outside retained range 0..%d", len(log.data))
			}
			end := min(offset+commandLogPageBytes, len(log.data))
			if end < len(log.data) {
				for n := 0; n < 3 && !utf8.RuneStart(log.data[end]); n++ {
					end--
				}
			}
			meta := map[string]any{"logId": id, "offset": offset}
			output := subproc.DecodeOutputBytes(log.data[offset:end])
			if end < len(log.data) {
				meta["nextOffset"] = end
				output += fmt.Sprintf("\n[next offset=%d]", end)
			} else {
				output += fmt.Sprintf("\n[end of retained log: %d/%d bytes]", end, log.total)
			}
			return ToolOutput{Title: "bash output " + id, Output: output, Metadata: meta}, nil
		}
	}
	return ToolOutput{IsError: true}, fmt.Errorf("foreground log unavailable in this turn (expired or unknown ID)")
}
