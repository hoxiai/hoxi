package runner

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
)

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
	cmdByStep      map[int]agyToolCall
	assigned       map[int]agyToolCall
	pending        []agyPendingEdit
}

func newAgyEditPayloadCursor(conversationID string) *agyEditPayloadCursor {
	return &agyEditPayloadCursor{
		conversationID: strings.TrimSpace(conversationID),
		byStep:         make(map[int]agyToolCall),
		cmdByStep:      make(map[int]agyToolCall),
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

func (c *agyEditPayloadCursor) lookupCommandSummary(stepIndex int, rawParams map[string]interface{}) map[string]interface{} {
	if c == nil || stepIndex <= 0 {
		return nil
	}
	c.pull()
	if call, ok := c.cmdByStep[stepIndex]; ok {
		return call.Args
	}
	if c.hasTranscriptFile() {
		time.Sleep(30 * time.Millisecond)
		c.pull()
		if call, ok := c.cmdByStep[stepIndex]; ok {
			return call.Args
		}
	}
	rawCmd := strings.TrimSpace(firstString(rawParams["CommandLine"], rawParams["command"], rawParams["cmd"]))
	if rawCmd != "" {
		for _, call := range c.cmdByStep {
			plannedCmd := strings.TrimSpace(firstString(call.Args["CommandLine"], call.Args["command"], call.Args["cmd"]))
			if plannedCmd == rawCmd {
				return call.Args
			}
		}
	}
	return nil
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
				if row.StepIndex == 0 {
					clear(c.byStep)
					clear(c.cmdByStep)
					clear(c.assigned)
				}
				continue
			}
			for i, call := range row.ToolCalls {
				if agyEditPayloadNames[call.Name] {
					call.Args = decodeAgyToolArgs(call.Args)
					execStep := row.StepIndex + 1 + i
					c.byStep[execStep] = call
					c.pending = append(c.pending, agyPendingEdit{execStep: execStep, call: call})
				} else if strings.EqualFold(call.Name, "run_command") {
					call.Args = decodeAgyToolArgs(call.Args)
					execStep := row.StepIndex + 1 + i
					c.cmdByStep[execStep] = call
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
	if next.Tool == "bash" && prev.Tool == "bash" {
		if firstString(next.State.Input["toolSummary"], next.State.Input["toolAction"], next.State.Input["description"]) == "" &&
			firstString(prev.State.Input["toolSummary"], prev.State.Input["toolAction"], prev.State.Input["description"]) != "" {
			next.State.Input = mergeAgyEditParams(next.State.Input, prev.State.Input)
		}
		if next.State.Title == "" && prev.State.Title != "" {
			next.State.Title = prev.State.Title
		}
	}
	if next.Tool == "question" && prev.Tool == "question" {
		if !hasQuestionsInput(next.State.Input) && hasQuestionsInput(prev.State.Input) {
			next.State.Input = mergeAgyEditParams(next.State.Input, prev.State.Input)
		}
		if len(prev.State.Metadata) > 0 {
			if next.State.Metadata == nil {
				next.State.Metadata = make(map[string]interface{}, len(prev.State.Metadata))
			}
			for k, v := range prev.State.Metadata {
				if _, exists := next.State.Metadata[k]; !exists {
					next.State.Metadata[k] = v
				}
			}
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
	if part.Type != "tool" || part.State == nil {
		return false
	}
	if _, ok := parseAgyToolStepIndex(part.ID); !ok {
		return false
	}
	if part.Tool == "bash" {
		return firstString(part.State.Input["toolSummary"], part.State.Input["toolAction"], part.State.Input["description"]) == ""
	}
	if part.Tool == "question" {
		return !hasQuestionsInput(part.State.Input)
	}
	if part.Tool != "edit" && part.Tool != "write" {
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
					if agyEditPayloadNames[call.Name] || strings.EqualFold(call.Name, "run_command") || isQuestionToolName(call.Name) {
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
// edit payload or command summary was omitted on stdout or truncated by compact transcript.jsonl.
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
			if part.Type != "tool" || part.State == nil {
				continue
			}
			stepIdx, ok := parseAgyToolStepIndex(part.ID)
			if !ok {
				continue
			}
			if part.Tool == "bash" {
				if call, found := byStep[stepIdx]; found && strings.EqualFold(call.Name, "run_command") && partNeedsAgyEditHydration(*part) {
					merged := mergeAgyEditParams(part.State.Input, call.Args)
					_, title, cleanInput := normalizeRunnerTool(call.Name, merged)
					if !reflect.DeepEqual(part.State.Input, cleanInput) || (part.State.Title == "" && title != "") {
						part.State.Input = cleanInput
						if part.State.Title == "" || part.State.Title == "run_command" {
							part.State.Title = title
						}
						changed = true
					}
				}
				continue
			}
			if part.Tool == "question" {
				if call, found := byStep[stepIdx]; found && isQuestionToolName(call.Name) && partNeedsAgyEditHydration(*part) {
					merged := mergeAgyEditParams(part.State.Input, call.Args)
					_, title, cleanInput := normalizeRunnerTool(call.Name, merged)
					if !reflect.DeepEqual(part.State.Input, cleanInput) {
						part.State.Input = cleanInput
						if part.State.Title == "" && title != "" {
							part.State.Title = title
						}
						changed = true
					}
				}
				continue
			}
			if part.Tool != "edit" && part.Tool != "write" {
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
