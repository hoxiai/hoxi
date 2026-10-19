package runner

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Antigravity keeps a per-conversation transcript under
// <gemini-home>/<product>/brain/<conversation-id>/.system_generated/logs/.
// It carries what stdout leaves out: thinking text, system message bodies and
// every step of subagents.

const agySubagentLogChunk = 1 << 20

type agyTranscriptRow struct {
	StepIndex int    `json:"step_index"`
	Type      string `json:"type"`
	Thinking  string `json:"thinking"`
	Content   string `json:"content"`
	ToolCalls []struct {
		Name string `json:"name"`
	} `json:"tool_calls"`
}

func agyBrainRoots() []string {
	gRoot := geminiHomeDir()
	if gRoot == "" {
		return nil
	}
	return []string{
		filepath.Join(gRoot, "antigravity-cli", "brain"),
		filepath.Join(gRoot, "antigravity", "brain"),
	}
}

func agyTranscriptLogDir(root, conversationID string) string {
	return filepath.Join(root, conversationID, ".system_generated", "logs")
}

// lookupAgyTranscriptRow returns the first row for stepIndex that satisfies keep.
func lookupAgyTranscriptRow(conversationID string, stepIndex int, keep func(agyTranscriptRow) bool) (agyTranscriptRow, bool) {
	if !validStoreID(conversationID) {
		return agyTranscriptRow{}, false
	}
	for _, root := range agyBrainRoots() {
		dir := agyTranscriptLogDir(root, conversationID)
		for _, name := range []string{"transcript_full.jsonl", "transcript.jsonl"} {
			f, err := os.Open(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
			for scanner.Scan() {
				var row agyTranscriptRow
				if json.Unmarshal(bytes.TrimSpace(scanner.Bytes()), &row) == nil && row.StepIndex == stepIndex && keep(row) {
					_ = f.Close()
					return row, true
				}
			}
			_ = f.Close()
		}
	}
	return agyTranscriptRow{}, false
}

func lookupAgyTranscriptThinking(conversationID string, stepIndex int) string {
	row, _ := lookupAgyTranscriptRow(conversationID, stepIndex, func(r agyTranscriptRow) bool {
		return strings.TrimSpace(r.Thinking) != ""
	})
	return strings.TrimSpace(row.Thinking)
}

// lookupAgyTranscriptContent retries briefly because stdout can announce a
// step before agy has flushed it to the transcript.
func lookupAgyTranscriptContent(conversationID string, stepIndex int) string {
	for attempt := 0; ; attempt++ {
		row, ok := lookupAgyTranscriptRow(conversationID, stepIndex, func(r agyTranscriptRow) bool {
			return strings.TrimSpace(r.Content) != ""
		})
		if ok || attempt == 2 {
			return strings.TrimSpace(row.Content)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// resolveAgySubagentLog trusts a reported log_uri only inside a brain root,
// and otherwise derives the transcript path from the conversation ID.
func resolveAgySubagentLog(logURI, conversationID string) string {
	roots := agyBrainRoots()
	if u, err := url.Parse(strings.TrimSpace(logURI)); err == nil && u.Scheme == "file" && u.Path != "" {
		p := filepath.Clean(u.Path)
		base := filepath.Base(p)
		if base == "transcript.jsonl" || base == "transcript_full.jsonl" {
			for _, root := range roots {
				if strings.HasPrefix(p, root+string(filepath.Separator)) {
					return p
				}
			}
		}
	}
	if !validStoreID(conversationID) {
		return ""
	}
	for _, root := range roots {
		p := filepath.Join(agyTranscriptLogDir(root, conversationID), "transcript.jsonl")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// advanceAgySubagentLog consumes rows appended since the last call and
// reports whether the subagent made progress.
func advanceAgySubagentLog(tr *agySubagentTrack) bool {
	st, err := os.Stat(tr.logPath)
	if err != nil {
		return false
	}
	if st.Size() < tr.readOffset {
		tr.readOffset, tr.steps = 0, 0
	}
	if st.Size() == tr.readOffset {
		return false
	}
	f, err := os.Open(tr.logPath)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, min(st.Size()-tr.readOffset, agySubagentLogChunk))
	n, err := f.ReadAt(buf, tr.readOffset)
	if err != nil && err != io.EOF {
		return false
	}
	buf = buf[:n]
	end := bytes.LastIndexByte(buf, '\n')
	if end < 0 {
		if len(buf) < agySubagentLogChunk {
			return false // the current row is still being written
		}
		tr.readOffset += int64(len(buf)) // a single oversized row; skip it
		return false
	}
	for _, line := range bytes.Split(buf[:end], []byte{'\n'}) {
		var row agyTranscriptRow
		if json.Unmarshal(bytes.TrimSpace(line), &row) != nil {
			continue
		}
		if row.StepIndex+1 > tr.steps {
			tr.steps = row.StepIndex + 1
		}
		if len(row.ToolCalls) > 0 {
			tr.currentTool = row.ToolCalls[len(row.ToolCalls)-1].Name
		}
	}
	tr.readOffset += int64(end + 1)
	tr.lastActivityMs = st.ModTime().UnixMilli()
	return true
}
