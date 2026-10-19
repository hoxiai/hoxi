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
	CreatedAt string `json:"created_at"`
	Thinking  string `json:"thinking"`
	Content   string `json:"content"`
	ToolCalls []struct {
		Name string                 `json:"name"`
		Args map[string]interface{} `json:"args"`
	} `json:"tool_calls"`
}

// decodeAgyToolArgs unwraps transcript tool arguments, which agy stores as
// JSON-encoded strings (`"CommandLine": "\"ls -la\""`).
func decodeAgyToolArgs(args map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(args))
	for k, v := range args {
		if s, ok := v.(string); ok {
			var decoded interface{}
			if json.Unmarshal([]byte(s), &decoded) == nil {
				v = decoded
			}
		}
		out[k] = v
	}
	return out
}

// stripAgyResultHeader drops the "Created At/Completed At" preamble of tool results.
func stripAgyResultHeader(content string) string {
	lines := strings.Split(content, "\n")
	i := 0
	for i < len(lines) && (strings.HasPrefix(lines[i], "Created At:") || strings.HasPrefix(lines[i], "Completed At:")) {
		i++
	}
	return strings.TrimSpace(strings.Join(lines[i:], "\n"))
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

// advanceAgySubagentLog returns the complete rows appended since the last
// call and the file's modification time; ok is false when nothing moved.
func advanceAgySubagentLog(tr *agySubagentTrack) (rows []agyTranscriptRow, modMs int64, ok bool) {
	st, err := os.Stat(tr.logPath)
	if err != nil {
		return nil, 0, false
	}
	if st.Size() < tr.readOffset {
		tr.readOffset = 0
	}
	if st.Size() == tr.readOffset {
		return nil, 0, false
	}
	f, err := os.Open(tr.logPath)
	if err != nil {
		return nil, 0, false
	}
	defer f.Close()
	buf := make([]byte, min(st.Size()-tr.readOffset, agySubagentLogChunk))
	n, err := f.ReadAt(buf, tr.readOffset)
	if err != nil && err != io.EOF {
		return nil, 0, false
	}
	buf = buf[:n]
	end := bytes.LastIndexByte(buf, '\n')
	if end < 0 {
		if len(buf) == agySubagentLogChunk {
			tr.readOffset += int64(len(buf)) // a single oversized row; skip it
		}
		return nil, 0, false // otherwise the current row is still being written
	}
	for _, line := range bytes.Split(buf[:end], []byte{'\n'}) {
		var row agyTranscriptRow
		if json.Unmarshal(bytes.TrimSpace(line), &row) == nil {
			rows = append(rows, row)
		}
	}
	tr.readOffset += int64(end + 1)
	return rows, st.ModTime().UnixMilli(), true
}
