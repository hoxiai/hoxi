package runner

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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
	"replace":                    true,
}

var agyEditPayloadKeys = []string{
	"TargetContent", "targetContent", "ReplacementContent", "replacementContent",
	"CodeContent", "codeContent", "Content", "content", "old_string", "new_string",
	"oldString", "newString", "Target", "Replacement",
}

type agyPendingEdit struct {
	execStep int
	call     agyToolCall
}

// agyEditPayloadCursor walks a conversation transcript in order and hands back
// the planned arguments of each edit, indexed by execution step (row.StepIndex+1+i)
// with an ordered per-turn fallback queue. Repeated lookups for the same stdout
// stepIndex (e.g. ACTIVE followed by DONE) return the exact same planned call.
type agyEditPayloadCursor struct {
	conversationID string
	activePath     string
	offset         int64
	byStep         map[int]agyToolCall
	assigned       map[int]agyToolCall
	pending        []agyPendingEdit
}

func newAgyEditPayloadCursor(conversationID string) *agyEditPayloadCursor {
	return &agyEditPayloadCursor{
		conversationID: strings.TrimSpace(conversationID),
		byStep:         make(map[int]agyToolCall),
		assigned:       make(map[int]agyToolCall),
	}
}

func (c *agyEditPayloadCursor) next() (string, map[string]interface{}) {
	return c.lookup(0, nil, false)
}

func (c *agyEditPayloadCursor) lookup(stepIndex int, rawParams map[string]interface{}, waitIfMissing bool) (string, map[string]interface{}) {
	if c == nil {
		return "", nil
	}
	if stepIndex > 0 {
		if call, ok := c.assigned[stepIndex]; ok {
			return call.Name, call.Args
		}
	}
	c.pull()
	if waitIfMissing {
		for attempt := 0; attempt < 2; attempt++ {
			if _, ok := c.byStep[stepIndex]; ok || len(c.pending) > 0 || !c.hasTranscriptFile() {
				break
			}
			time.Sleep(50 * time.Millisecond)
			c.pull()
		}
	}
	if stepIndex > 0 {
		if call, ok := c.byStep[stepIndex]; ok {
			c.assigned[stepIndex] = call
			c.removePendingByStep(stepIndex)
			return call.Name, call.Args
		}
	}
	if len(c.pending) == 0 {
		return "", nil
	}
	pickIdx := 0
	for i, entry := range c.pending {
		if agyPathsMatch(rawParams, entry.call.Args) {
			pickIdx = i
			break
		}
	}
	call := c.pending[pickIdx].call
	c.pending = append(c.pending[:pickIdx], c.pending[pickIdx+1:]...)
	if stepIndex > 0 {
		c.assigned[stepIndex] = call
	}
	return call.Name, call.Args
}

func (c *agyEditPayloadCursor) removePendingByStep(execStep int) {
	for i, entry := range c.pending {
		if entry.execStep == execStep {
			c.pending = append(c.pending[:i], c.pending[i+1:]...)
			return
		}
	}
}

func (c *agyEditPayloadCursor) hasTranscriptFile() bool {
	if c.conversationID == "" || !validStoreID(c.conversationID) {
		return false
	}
	for _, root := range agyBrainRoots() {
		dir := agyTranscriptLogDir(root, c.conversationID)
		for _, name := range []string{"transcript_full.jsonl", "transcript.jsonl"} {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				return true
			}
		}
	}
	return false
}

// pull reads transcript rows appended since the last call. transcript_full.jsonl
// is preferred over transcript.jsonl because compact transcript.jsonl truncates
// large tool_calls arguments with `<truncated N bytes>`.
func (c *agyEditPayloadCursor) pull() {
	if c.conversationID == "" || !validStoreID(c.conversationID) {
		return
	}
	var candidate string
	for _, root := range agyBrainRoots() {
		dir := agyTranscriptLogDir(root, c.conversationID)
		for _, name := range []string{"transcript_full.jsonl", "transcript.jsonl"} {
			p := filepath.Join(dir, name)
			if st, err := os.Stat(p); err == nil && st.Size() > 0 {
				candidate = p
				break
			}
		}
		if candidate != "" {
			break
		}
	}
	if candidate == "" {
		return
	}
	if candidate != c.activePath {
		c.activePath = candidate
		c.offset = 0
		c.pending = nil
	}
	f, err := os.Open(candidate)
	if err != nil {
		return
	}
	defer f.Close()
	size := fileSize(f)
	if size < c.offset {
		c.offset = 0
		c.pending = nil
	}
	if size == c.offset {
		return
	}
	for c.offset < size {
		buf := make([]byte, min(size-c.offset, agySubagentLogChunk))
		n, err := f.ReadAt(buf, c.offset)
		if err != nil && err != io.EOF {
			return
		}
		buf = buf[:n]
		end := bytes.LastIndexByte(buf, '\n')
		if end < 0 {
			if len(buf) == agySubagentLogChunk {
				c.offset += int64(len(buf))
				continue
			}
			return
		}
		for _, line := range bytes.Split(buf[:end], []byte{'\n'}) {
			var row agyTranscriptRow
			if json.Unmarshal(bytes.TrimSpace(line), &row) != nil {
				continue
			}
			if row.Type == "USER_INPUT" {
				c.pending = nil
				continue
			}
			for i, call := range row.ToolCalls {
				if agyEditPayloadNames[call.Name] {
					call.Args = decodeAgyToolArgs(call.Args)
					execStep := row.StepIndex + 1 + i
					c.byStep[execStep] = call
					c.pending = append(c.pending, agyPendingEdit{execStep: execStep, call: call})
				}
			}
		}
		c.offset += int64(end + 1)
	}
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
	for _, key := range agyEditPayloadKeys {
		if s, ok := params[key].(string); ok && strings.TrimSpace(s) != "" {
			return false
		}
	}
	if chunks, ok := params["ReplacementChunks"].([]interface{}); ok && len(chunks) > 0 {
		return false
	}
	return true
}

func extractAgyParamPath(params map[string]interface{}) string {
	if len(params) == 0 {
		return ""
	}
	return firstString(
		params["filePath"],
		params["Path"],
		params["AbsolutePath"],
		params["TargetFile"],
		params["file_path"],
		params["target_file"],
		params["path"],
	)
}

func agyPathsMatch(a, b map[string]interface{}) bool {
	pa := filepath.ToSlash(strings.TrimSpace(extractAgyParamPath(a)))
	pb := filepath.ToSlash(strings.TrimSpace(extractAgyParamPath(b)))
	if pa == "" || pb == "" {
		return false
	}
	if pa == pb {
		return true
	}
	return strings.HasSuffix(pa, "/"+pb) || strings.HasSuffix(pb, "/"+pa)
}

func preserveToolEditInput(next *MessagePart, prev MessagePart) {
	if next == nil || next.Type != "tool" || prev.Type != "tool" || next.State == nil || prev.State == nil {
		return
	}
	if agyParamsLackEditPayload(next.State.Input) && !agyParamsLackEditPayload(prev.State.Input) {
		next.State.Input = mergeAgyEditParams(next.State.Input, prev.State.Input)
		if (next.Tool == "edit" || next.Tool == "") && prev.Tool == "write" {
			next.Tool = prev.Tool
		}
	}
}

func parseAgyToolStepIndex(partID string) (int, bool) {
	idx := strings.LastIndex(partID, "_tool_")
	if idx < 0 {
		return 0, false
	}
	rest := partID[idx+len("_tool_"):]
	if rest == "" || strings.Contains(rest, "_") {
		return 0, false
	}
	step, err := strconv.Atoi(rest)
	if err != nil || step <= 0 {
		return 0, false
	}
	return step, true
}

func partHasTruncatedOrQuotedEditPayload(input map[string]interface{}) bool {
	for _, key := range agyEditPayloadKeys {
		if s, ok := input[key].(string); ok {
			trimmed := strings.TrimSpace(s)
			if strings.HasPrefix(trimmed, "\"") || strings.Contains(trimmed, "<truncated ") {
				return true
			}
		}
	}
	return false
}

func partNeedsAgyEditHydration(part MessagePart) bool {
	if part.Type != "tool" || part.State == nil || (part.Tool != "edit" && part.Tool != "write") {
		return false
	}
	if _, ok := parseAgyToolStepIndex(part.ID); !ok {
		return false
	}
	return agyParamsLackEditPayload(part.State.Input) || partHasTruncatedOrQuotedEditPayload(part.State.Input)
}

func loadAgyTranscriptEditsByStep(conversationID string) map[int]agyToolCall {
	if !validStoreID(conversationID) {
		return nil
	}
	for _, root := range agyBrainRoots() {
		dir := agyTranscriptLogDir(root, conversationID)
		for _, name := range []string{"transcript_full.jsonl", "transcript.jsonl"} {
			f, err := os.Open(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			byStep := make(map[int]agyToolCall)
			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
			for scanner.Scan() {
				var row agyTranscriptRow
				if json.Unmarshal(bytes.TrimSpace(scanner.Bytes()), &row) != nil {
					continue
				}
				for i, call := range row.ToolCalls {
					if agyEditPayloadNames[call.Name] {
						call.Args = decodeAgyToolArgs(call.Args)
						byStep[row.StepIndex+1+i] = call
					}
				}
			}
			_ = f.Close()
			if len(byStep) > 0 {
				return byStep
			}
		}
	}
	return nil
}

// hydrateAgyTranscriptEditParts repairs persisted Antigravity tool parts whose
// edit payload was omitted on DONE or truncated by compact transcript.jsonl.
func hydrateAgyTranscriptEditParts(externalID string, turns []MessageTurn) bool {
	needsRepair := false
	for i := range turns {
		for j := range turns[i].Parts {
			if partNeedsAgyEditHydration(turns[i].Parts[j]) {
				needsRepair = true
				break
			}
		}
		if needsRepair {
			break
		}
	}
	if !needsRepair {
		return false
	}
	byStep := loadAgyTranscriptEditsByStep(strings.TrimSpace(externalID))
	changed := false
	for i := range turns {
		for j := range turns[i].Parts {
			part := &turns[i].Parts[j]
			if part.Type != "tool" || part.State == nil || (part.Tool != "edit" && part.Tool != "write") {
				continue
			}
			stepIdx, ok := parseAgyToolStepIndex(part.ID)
			if !ok {
				continue
			}
			if call, found := byStep[stepIdx]; found && (partNeedsAgyEditHydration(*part) || agyPathsMatch(part.State.Input, call.Args)) {
				stdoutOnly := make(map[string]interface{}, 2)
				for _, pk := range []string{"Path", "filePath", "TargetFile", "AbsolutePath"} {
					if v, exists := part.State.Input[pk]; exists {
						stdoutOnly[pk] = v
					}
				}
				merged := mergeAgyEditParams(stdoutOnly, call.Args)
				normTool, title, cleanInput := normalizeRunnerTool(call.Name, merged)
				if part.Tool != normTool || !reflect.DeepEqual(part.State.Input, cleanInput) {
					part.Tool = normTool
					part.State.Input = cleanInput
					if part.State.Title == "" || part.State.Title == "edit_file" || part.State.Title == "write_file" {
						part.State.Title = title
					}
					changed = true
				}
				continue
			}
			if partHasTruncatedOrQuotedEditPayload(part.State.Input) {
				decoded := decodeAgyToolArgs(part.State.Input)
				if !reflect.DeepEqual(part.State.Input, decoded) {
					part.State.Input = decoded
					changed = true
				}
			}
		}
	}
	return changed
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
