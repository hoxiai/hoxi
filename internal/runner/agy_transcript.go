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
	StepIndex int           `json:"step_index"`
	Type      string        `json:"type"`
	CreatedAt string        `json:"created_at"`
	Thinking  string        `json:"thinking"`
	Content   string        `json:"content"`
	ToolCalls []agyToolCall `json:"tool_calls"`
}

// agyToolCall is one planned tool invocation recorded in a transcript row.
// One PLANNER_RESPONSE can carry several; agy then runs them one stdout step
// at a time, and those steps only repeat the file path.
type agyToolCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
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

// agyEditPayloadNames are the tools whose stdout steps carry only the file
// path; the text being written lives in the transcript instead.
var agyEditPayloadNames = map[string]bool{
	"edit_file":                  true,
	"write_file":                 true,
	"write_to_file":              true,
	"replace_file_content":       true,
	"multi_replace_file_content": true,
	"sed_file":                   true,
}

// agyEditPayloadCursor walks a conversation transcript in order and hands back
// the planned arguments of each edit, one per stdout step. agy runs the calls
// of one PLANNER_RESPONSE as consecutive steps and never reorders them.
type agyEditPayloadCursor struct {
	conversationID string
	offset         int64
	pending        []agyToolCall
}

func newAgyEditPayloadCursor(conversationID string) *agyEditPayloadCursor {
	return &agyEditPayloadCursor{conversationID: strings.TrimSpace(conversationID)}
}

func (c *agyEditPayloadCursor) next() (string, map[string]interface{}) {
	c.pull()
	if len(c.pending) == 0 {
		return "", nil
	}
	call := c.pending[0]
	c.pending = c.pending[1:]
	return call.Name, call.Args
}

// pull reads transcript rows appended since the last call. transcript.jsonl is
// read before transcript_full.jsonl, which repeats the same rows.
func (c *agyEditPayloadCursor) pull() {
	if c.conversationID == "" || !validStoreID(c.conversationID) {
		return
	}
	var path string
	for _, root := range agyBrainRoots() {
		dir := agyTranscriptLogDir(root, c.conversationID)
		for _, name := range []string{"transcript.jsonl", "transcript_full.jsonl"} {
			candidate := filepath.Join(dir, name)
			if st, err := os.Stat(candidate); err == nil && st.Size() > c.offset {
				path = candidate
				break
			}
		}
		if path != "" {
			break
		}
	}
	if path == "" {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	buf := make([]byte, min(fileSize(f)-c.offset, agySubagentLogChunk))
	n, err := f.ReadAt(buf, c.offset)
	if err != nil && err != io.EOF {
		return
	}
	buf = buf[:n]
	end := bytes.LastIndexByte(buf, '\n')
	if end < 0 {
		if len(buf) == agySubagentLogChunk {
			c.offset += int64(len(buf))
		}
		return
	}
	for _, line := range bytes.Split(buf[:end], []byte{'\n'}) {
		var row agyTranscriptRow
		if json.Unmarshal(bytes.TrimSpace(line), &row) != nil {
			continue
		}
		for _, call := range row.ToolCalls {
			if agyEditPayloadNames[call.Name] {
				call.Args = decodeAgyToolArgs(call.Args)
				c.pending = append(c.pending, call)
			}
		}
	}
	c.offset += int64(end + 1)
}

func fileSize(f *os.File) int64 {
	st, err := f.Stat()
	if err != nil {
		return 0
	}
	return st.Size()
}

// mergeAgyEditParams fills the gaps in what stdout reported from the planned
// call. Stdout wins where both have a value, so a path agy rewrote at
// execution time is never replaced by the planned one.
func mergeAgyEditParams(stdout, planned map[string]interface{}) map[string]interface{} {
	if len(stdout) == 0 {
		return planned
	}
	merged := make(map[string]interface{}, len(stdout)+len(planned))
	for k, v := range planned {
		merged[k] = v
	}
	for k, v := range stdout {
		if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
			continue
		}
		merged[k] = v
	}
	return merged
}

// agyParamsLackEditPayload reports whether stdout omitted the text of an edit,
// which is all current agy sends for edit_file and write_file.
func agyParamsLackEditPayload(params map[string]interface{}) bool {
	for _, key := range []string{
		"TargetContent", "targetContent", "ReplacementContent", "replacementContent",
		"CodeContent", "codeContent", "Content", "content", "old_string", "new_string",
		"oldString", "newString", "Target", "Replacement",
	} {
		if s, ok := params[key].(string); ok && strings.TrimSpace(s) != "" {
			return false
		}
	}
	return true
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
