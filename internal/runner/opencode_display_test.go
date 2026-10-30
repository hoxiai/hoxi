package runner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

const displayTestMarker = "# hoxi-stop:ses_engine:call_1"
const displayTestCommand = displayTestMarker + "\ntrap : EXIT\ngit status\nprintf 'done\\n'"

func displayTestPart() MessagePart {
	return MessagePart{
		ID: "prt_1", SessionID: "ses_engine", MessageID: "msg_1", CallID: "call_1", Type: "tool", Tool: "bash",
		State: &ToolState{
			Status: "completed", Input: map[string]interface{}{"command": displayTestCommand},
			Title: strings.TrimPrefix(displayTestMarker, "# "), Output: displayTestMarker,
			Metadata: map[string]interface{}{"command": displayTestCommand, "output": displayTestMarker},
		},
	}
}

func assertCleanDisplayPart(t *testing.T, part MessagePart) {
	t.Helper()
	if part.State.Input["command"] != "git status\nprintf 'done\\n'" || part.State.Title != "git status" {
		t.Fatalf("internal marker leaked into display: %+v", part.State)
	}
	if part.State.Metadata["command"] != part.State.Input["command"] {
		t.Fatalf("metadata command was not normalized: %+v", part.State.Metadata)
	}
	if part.ID != "prt_1" || part.SessionID != "ses_engine" || part.CallID != "call_1" {
		t.Fatalf("stop identity changed: %+v", part)
	}
	if part.State.Output != displayTestMarker || part.State.Metadata["output"] != displayTestMarker {
		t.Fatalf("real output was rewritten: %+v", part.State)
	}
}

func TestOpenCodeDisplayNormalizesManagedToolParts(t *testing.T) {
	t.Parallel()
	part := displayTestPart()
	stripOpenCodeStopMarker(&part)
	assertCleanDisplayPart(t, part)
	// Display cleanup is idempotent across live and history normalization.
	stripOpenCodeStopMarker(&part)
	assertCleanDisplayPart(t, part)

	for _, prefix := range []string{
		displayTestMarker + "\ntrap : EXIT\n",
		displayTestMarker + "\r\ntrap : EXIT\r\n",
		"trap : EXIT " + displayTestMarker + "\n",
		"trap : EXIT\n",
		openCodeStopTrap + "\n",
		openCodeStopTrapUSR1 + "\n",
	} {
		part := displayTestPart()
		part.State.Title = prefix + "Inspect working tree"
		part.State.Input["description"] = prefix + "Inspect working tree"
		stripOpenCodeStopMarker(&part)
		if part.State.Title != "Inspect working tree" || part.State.Input["description"] != "Inspect working tree" {
			t.Fatalf("description retained a prefix: %+v", part.State)
		}
	}

	wrappedOnce := displayTestMarker + "\n" + openCodeStopTrap + "\n'/usr/bin/sandbox-exec' -f '/tmp/p.sb' -- /bin/sh -c 'git status'"
	wrappedNested := displayTestMarker + "\n" + openCodeStopTrap + "\n'/usr/bin/sandbox-exec' -f '/tmp/p.sb' -- /bin/sh -c '" +
		strings.ReplaceAll(wrappedOnce, "'", `'\''`) + "'"
	nestedPart := MessagePart{
		ID: "prt_nested", SessionID: "ses_engine", MessageID: "msg_1", CallID: "call_nested", Type: "tool", Tool: "bash",
		State: &ToolState{
			Status: "completed",
			Input:  map[string]interface{}{"command": wrappedNested},
			Title:  wrappedNested,
		},
	}
	stripOpenCodeStopMarker(&nestedPart)
	if nestedPart.State.Input["command"] != "git status" || nestedPart.State.Title != "git status" {
		t.Fatalf("sandbox wrapper leaked into display: %+v", nestedPart.State)
	}
}

func TestOpenCodeDisplayJSONPreservesContentAndNumbers(t *testing.T) {
	t.Parallel()
	part := displayTestPart()
	part.State.Metadata["sequence"] = json.Number("9007199254740993")
	text := MessagePart{ID: "prt_text", Type: "text", Text: displayTestCommand}
	raw, err := json.Marshal([]MessageTurn{{Parts: []MessagePart{part, text}}})
	if err != nil {
		t.Fatal(err)
	}
	cleaned := SanitizeOpenCodeToolJSON(raw)
	var turns []MessageTurn
	dec := json.NewDecoder(strings.NewReader(string(cleaned)))
	dec.UseNumber()
	if err := dec.Decode(&turns); err != nil {
		t.Fatal(err)
	}
	assertCleanDisplayPart(t, turns[0].Parts[0])
	if turns[0].Parts[1].Text != displayTestCommand || turns[0].Parts[0].State.Metadata["sequence"] != json.Number("9007199254740993") {
		t.Fatalf("non-display content changed: %s", cleaned)
	}
	if got := string(SanitizeOpenCodeToolJSON(cleaned)); got != string(cleaned) {
		t.Fatalf("cleanup is not idempotent: %s", got)
	}
}

func TestOpenCodeDisplayLeavesLiteralMarkersAndOtherToolsUntouched(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		` {"type":"tool","tool":"bash","state":{"input":{"command":"echo '# hoxi-stop:example'"},"output":"# hoxi-stop:example"}} `,
		`{"type":"tool","tool":"read","state":{"input":{"command":"# hoxi-stop:example"}}}`,
		`{"type":"message.part.delta","properties":{"delta":"# hoxi-stop:example"}}`,
		`{"type":"tool","tool":"bash","state":{"input":{"example":{"parts":[{"type":"tool","tool":"bash","state":{"title":"hoxi-stop:example"}}]}}}}`,
		`{"type":"text","text":"# hoxi-stop:example"}`,
		`{"hoxi-stop:invalid JSON`,
	} {
		if got := string(SanitizeOpenCodeToolJSON([]byte(raw))); got != raw {
			t.Fatalf("non-display content changed:\nwant %s\ngot %s", raw, got)
		}
	}
}

func TestOpenCodeDisplaySSECoversDirectoryAndGlobalStreams(t *testing.T) {
	t.Parallel()
	for _, wrapped := range []bool{false, true} {
		event := map[string]interface{}{"type": "message.part.updated", "properties": map[string]interface{}{"part": displayTestPart()}}
		if wrapped {
			event = map[string]interface{}{"directory": "/tmp/worktree", "payload": event}
		}
		raw, _ := json.Marshal(event)
		line := SanitizeOpenCodeDisplaySSELine("data: " + string(raw) + "\r\n")
		if !strings.HasSuffix(line, "\r\n") {
			t.Fatalf("lost SSE boundary: %q", line)
		}
		root := decodeTrimmedPayload(t, line)
		if wrapped {
			root = root["payload"].(map[string]interface{})
		}
		partJSON, _ := json.Marshal(root["properties"].(map[string]interface{})["part"])
		var part MessagePart
		if err := json.Unmarshal(partJSON, &part); err != nil {
			t.Fatal(err)
		}
		assertCleanDisplayPart(t, part)
	}
	for _, frame := range []string{": keepalive\n", "data: not-json hoxi-stop:\n", "data: {\"type\":\"message.part.delta\",\"properties\":{\"delta\":\"hoxi-stop:example\"}}\n"} {
		if got := SanitizeOpenCodeDisplaySSELine(frame); got != frame {
			t.Fatalf("unrelated frame changed: %q", got)
		}
	}
	permission := `data: {"type":"permission.asked","properties":{"permission":"bash","patterns":["# hoxi-stop:ses_engine:call_1\ntrap : EXIT\ngit status"],"metadata":{"command":"# hoxi-stop:ses_engine:call_1\ntrap : EXIT\ngit status"}}}` + "\n"
	if got := SanitizeOpenCodeDisplaySSELine(permission); strings.Contains(got, "hoxi-stop:") || !strings.Contains(got, "git status") {
		t.Fatalf("permission display regressed: %s", got)
	}
}

func TestOpenCodeDisplayCleansPersistedHistoryWithoutRewritingStore(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions"))
	if err := svc.store.Put(SessionRecord{
		ID: "ses_hoxi_display", Runtime: "opencode", ProjectDir: root, WorktreePath: root, SandboxMode: "local",
		Turns: []MessageTurn{{Parts: []MessagePart{displayTestPart()}}},
	}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	svc.handleSessionSubroutes(w, httptest.NewRequest(http.MethodGet, "/api/hoxi/runner/sessions/ses_hoxi_display/message", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("history failed: %d %s", w.Code, w.Body.String())
	}
	var turns []MessageTurn
	if err := json.Unmarshal(w.Body.Bytes(), &turns); err != nil {
		t.Fatal(err)
	}
	// History drops the identical metadata copy of the output (see history_page.go).
	part := turns[0].Parts[0]
	if _, dup := part.State.Metadata["output"]; dup {
		t.Fatalf("duplicate shell output kept in history: %+v", part.State.Metadata)
	}
	part.State.Metadata["output"] = part.State.Output
	assertCleanDisplayPart(t, part)
	stored, _ := svc.store.Get("ses_hoxi_display")
	if stored.Turns[0].Parts[0].State.Input["command"] != displayTestCommand {
		t.Fatal("reading history modified the persisted record")
	}
}
