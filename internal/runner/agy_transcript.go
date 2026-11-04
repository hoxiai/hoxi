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
	StepIndex       int           `json:"step_index"`
	Type            string        `json:"type"`
	CreatedAt       string        `json:"created_at"`
	Thinking        string        `json:"thinking"`
	Content         string        `json:"content"`
	ToolCalls       []agyToolCall `json:"tool_calls"`
	InputTokens     int           `json:"input_tokens"`
	OutputTokens    int           `json:"output_tokens"`
	ThinkingTokens  int           `json:"thinking_tokens"`
	CacheReadTokens int           `json:"cache_read_tokens"`
}

// agyToolCall is one planned tool invocation recorded in a transcript row.
// One PLANNER_RESPONSE can carry several; agy then runs them one stdout step
// at a time, and those steps only repeat the file path.
type agyToolCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

var agyStringOnlyArgNames = map[string]bool{
	"Content":            true,
	"content":            true,
	"Target":             true,
	"target":             true,
	"Replacement":        true,
	"replacement":        true,
	"TargetContent":      true,
	"targetContent":      true,
	"ReplacementContent": true,
	"replacementContent": true,
	"CodeContent":        true,
	"codeContent":        true,
	"old_string":         true,
	"new_string":         true,
	"oldString":          true,
	"newString":          true,
	"Path":               true,
	"path":               true,
	"AbsolutePath":       true,
	"TargetFile":         true,
	"CommandLine":        true,
	"toolSummary":        true,
}

func decodeAgyStringLiteral(s string) (string, bool) {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "\"") {
		return "", false
	}
	var unquoted string
	if json.Unmarshal([]byte(trimmed), &unquoted) == nil {
		return unquoted, true
	}
	if idx := strings.LastIndex(trimmed, "<truncated "); idx > 0 && strings.HasSuffix(trimmed, "bytes>") {
		prefix := strings.TrimRight(trimmed[:idx], "\r\n")
		bs := 0
		for i := len(prefix) - 1; i >= 0 && prefix[i] == '\\'; i-- {
			bs++
		}
		if bs%2 == 1 {
			prefix = prefix[:len(prefix)-1]
		}
		if json.Unmarshal([]byte(prefix+"\""), &unquoted) == nil {
			return unquoted, true
		}
	}
	return "", false
}

// decodeAgyToolArgs unwraps transcript tool arguments, which compact
// transcript.jsonl stores as JSON-encoded strings (`"CommandLine": "\"ls -la\""`),
// while keeping JSON file contents in transcript_full.jsonl as strings.
func decodeAgyToolArgs(args map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(args))
	for k, v := range args {
		if s, ok := v.(string); ok {
			if unquoted, ok := decodeAgyStringLiteral(s); ok {
				v = unquoted
			} else if agyStringOnlyArgNames[k] {
				if idx := strings.LastIndex(s, "\n<truncated "); idx >= 0 && strings.HasSuffix(strings.TrimSpace(s[idx:]), "bytes>") {
					s = s[:idx]
				}
				v = s
			} else {
				var decoded interface{}
				if json.Unmarshal([]byte(s), &decoded) == nil {
					v = decoded
				}
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

func agyConversationRoots() []string {
	gRoot := geminiHomeDir()
	if gRoot == "" {
		return nil
	}
	return []string{
		filepath.Join(gRoot, "antigravity-cli", "conversations"),
		filepath.Join(gRoot, "antigravity", "conversations"),
	}
}

// hasAgySplitBrainConversation reports whether an Antigravity conversation
// wrote its initial trajectory to the legacy Protobuf store (<id>.pb) while
// leaving a competing SQLite store (<id>.db) on disk (caused by a schema-init
// SQLite lock race in agy). Resuming such a conversation via --conversation
// opens the SQLite store and drops the Protobuf trajectory.
func hasAgySplitBrainConversation(conversationID string) bool {
	conversationID = strings.TrimSpace(conversationID)
	if !validStoreID(conversationID) {
		return false
	}
	for _, root := range agyConversationRoots() {
		pbInfo, pbErr := os.Stat(filepath.Join(root, conversationID+".pb"))
		if pbErr != nil || pbInfo.IsDir() || pbInfo.Size() == 0 {
			continue
		}
		dbInfo, dbErr := os.Stat(filepath.Join(root, conversationID+".db"))
		if dbErr == nil && !dbInfo.IsDir() && dbInfo.Size() > 0 {
			return true
		}
	}
	return false
}

func agyTranscriptLogDir(root, conversationID string) string {
	return filepath.Join(root, conversationID, ".system_generated", "logs")
}

// lookupAgyTranscriptRow returns the latest row for stepIndex in the active
// trajectory segment (after any step_index=0 USER_INPUT reset) that satisfies keep.
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
			var lastRow agyTranscriptRow
			matched := false
			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
			for scanner.Scan() {
				var row agyTranscriptRow
				if json.Unmarshal(bytes.TrimSpace(scanner.Bytes()), &row) != nil {
					continue
				}
				if row.StepIndex == 0 && row.Type == "USER_INPUT" {
					lastRow = agyTranscriptRow{}
					matched = false
				}
				if row.StepIndex == stepIndex && keep(row) {
					lastRow = row
					matched = true
				}
			}
			_ = f.Close()
			if matched {
				return lastRow, true
			}
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
					if base == "transcript.jsonl" {
						full := filepath.Join(filepath.Dir(p), "transcript_full.jsonl")
						if _, err := os.Stat(full); err == nil {
							return full
						}
					}
					return p
				}
			}
		}
	}
	if !validStoreID(conversationID) {
		return ""
	}
	for _, root := range roots {
		dir := agyTranscriptLogDir(root, conversationID)
		for _, name := range []string{"transcript_full.jsonl", "transcript.jsonl"} {
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err == nil {
				return p
			}
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

// IsStaleAgyResultError reports whether a stream `result` line is a historical
// trajectory error re-emitted by `agy` on a resumed conversation after the
// current turn has already finished its final assistant response cleanly.
func (t *TurnTranslator) IsStaleAgyResultError(rawLine string) bool {
	line := strings.TrimSpace(rawLine)
	if line == "" || !strings.HasPrefix(line, "{") {
		return false
	}
	var env agyRawEvent
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		return false
	}
	if env.Event != "result" || env.Result == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if id := strings.TrimSpace(firstNonEmptyStr(env.Result.ConversationID, env.ConversationID)); id != "" && t.convPinned && id != t.externalID {
		return false
	}
	return t.isStaleAgyResultErrorLocked(env.Result)
}

func (t *TurnTranslator) isStaleAgyResultErrorLocked(res *agyResult) bool {
	if res == nil || res.Status != "ERROR" || !t.agyFinalResponseDone {
		return false
	}
	if len(res.DeniedActions) > 0 || t.hasWaitingSubagents() {
		return false
	}
	errMsg := strings.TrimSpace(res.Error)
	if strings.EqualFold(errMsg, "interrupted") || !isRetryableTurnError(errMsg) {
		return false
	}
	for _, p := range t.partsByID {
		if p != nil && p.Type == "tool" && p.State != nil {
			switch p.State.Status {
			case "pending", "running", "error":
				return false
			}
		}
	}
	return true
}
