package runner

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func historyTestTurns(n int) []MessageTurn {
	turns := make([]MessageTurn, n)
	for i := range turns {
		role := "assistant"
		if i%3 == 0 {
			role = "user"
		}
		id := fmt.Sprintf("msg_%03d", i)
		turns[i] = MessageTurn{
			Info:  MessageInfo{ID: id, SessionID: "ses_hoxi_page", Role: role},
			Parts: []MessagePart{{ID: "prt_" + id, SessionID: "ses_hoxi_page", MessageID: id, Type: "text", Text: id}},
		}
	}
	return turns
}

func fetchHistory(t *testing.T, svc *Service, query string) []MessageTurn {
	t.Helper()
	w := httptest.NewRecorder()
	svc.handleSessionSubroutes(w, httptest.NewRequest(http.MethodGet, "/api/hoxi/runner/sessions/ses_hoxi_page/message"+query, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("history %q failed: %d %s", query, w.Code, w.Body.String())
	}
	var turns []MessageTurn
	if err := json.Unmarshal(w.Body.Bytes(), &turns); err != nil {
		t.Fatal(err)
	}
	return turns
}

func turnIDs(turns []MessageTurn) []string {
	ids := make([]string, len(turns))
	for i := range turns {
		ids[i] = turns[i].Info.ID
	}
	return ids
}

func TestHistoryPagingReturnsTailAndOlderPages(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions"))
	if err := svc.store.Put(SessionRecord{
		ID: "ses_hoxi_page", Runtime: "opencode", ProjectDir: root, WorktreePath: root,
		Turns: historyTestTurns(10),
	}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		query string
		want  []string
	}{
		{"", turnIDs(historyTestTurns(10))},
		{"?limit=0", turnIDs(historyTestTurns(10))},
		{"?limit=bogus", turnIDs(historyTestTurns(10))},
		{"?limit=3", []string{"msg_007", "msg_008", "msg_009"}},
		{"?limit=3&before=msg_007", []string{"msg_004", "msg_005", "msg_006"}},
		{"?limit=3&before=msg_002", []string{"msg_000", "msg_001"}},
		{"?limit=3&before=msg_000", []string{}},
		{"?limit=50", turnIDs(historyTestTurns(10))},
		{"?before=msg_002", []string{"msg_000", "msg_001"}},
		// A cursor the server no longer has must not re-send the tail as "older".
		{"?limit=3&before=msg_gone", []string{}},
	}
	for _, tc := range cases {
		got := turnIDs(fetchHistory(t, svc, tc.query))
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("query %q = %v, want %v", tc.query, got, tc.want)
		}
	}
}

func TestParseHistoryPageCapsLimit(t *testing.T) {
	t.Parallel()
	page := parseHistoryPage(map[string][]string{"limit": {"999999"}})
	if page.limit != maxHistoryPageTurns {
		t.Fatalf("limit = %d, want cap %d", page.limit, maxHistoryPageTurns)
	}
	if page := parseHistoryPage(map[string][]string{"limit": {"-4"}}); page.limit != 0 {
		t.Fatalf("negative limit = %d, want full history", page.limit)
	}
}

func TestHistoryDropsDuplicateShellOutputWithoutRewritingStore(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions"))
	tool := func(id, status, output string, meta map[string]interface{}) MessagePart {
		return MessagePart{
			ID: id, SessionID: "ses_hoxi_page", MessageID: "msg_tool", Type: "tool", Tool: "bash",
			State: &ToolState{Status: status, Output: output, Metadata: meta},
		}
	}
	if err := svc.store.Put(SessionRecord{
		ID: "ses_hoxi_page", Runtime: "opencode", ProjectDir: root, WorktreePath: root,
		Turns: []MessageTurn{{
			Info: MessageInfo{ID: "msg_tool", SessionID: "ses_hoxi_page", Role: "assistant"},
			Parts: []MessagePart{
				tool("prt_same", "completed", "ok\n", map[string]interface{}{"output": "ok\n", "exit": 0.0}),
				tool("prt_diff", "completed", "truncated", map[string]interface{}{"output": "full output"}),
				tool("prt_running", "running", "", map[string]interface{}{"output": "partial"}),
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	parts := fetchHistory(t, svc, "?limit=10")[0].Parts
	if _, ok := parts[0].State.Metadata["output"]; ok || parts[0].State.Output != "ok\n" || parts[0].State.Metadata["exit"] != 0.0 {
		t.Fatalf("duplicate output kept or other metadata lost: %+v", parts[0].State)
	}
	if parts[1].State.Metadata["output"] != "full output" {
		t.Fatalf("distinct metadata output dropped: %+v", parts[1].State)
	}
	if parts[2].State.Metadata["output"] != "partial" {
		t.Fatalf("running output dropped: %+v", parts[2].State)
	}
	stored, _ := svc.store.Get("ses_hoxi_page")
	if stored.Turns[0].Parts[0].State.Metadata["output"] != "ok\n" {
		t.Fatal("reading history modified the persisted record")
	}
}
