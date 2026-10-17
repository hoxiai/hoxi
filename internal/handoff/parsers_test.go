package handoff

import (
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func runSQLite(t *testing.T, dbPath, sql string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("mkdir for sqlite db: %v", err)
	}
	cmd := exec.Command("sqlite3", dbPath)
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 failed (%v): %s", err, string(out))
	}
}

func setupSyntheticCLIHome(t *testing.T, workspaceDir string) string {
	t.Helper()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("GEMINI_HOME", "")
	t.Setenv("ANTIGRAVITY_HOME", "")
	t.Setenv("GROK_HOME", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_GENERATIVE_AI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	// 1. Claude Code fixture
	claudeDir := filepath.Join(homeDir, ".claude")
	claudeProjDir := filepath.Join(claudeDir, "projects", encodeClaudeProjectHint(workspaceDir))
	if err := os.MkdirAll(claudeProjDir, 0o755); err != nil {
		t.Fatalf("mkdir claude proj: %v", err)
	}
	claudeSettings := map[string]interface{}{
		"model": "claude-sonnet-4-5",
		"env": map[string]interface{}{
			"ANTHROPIC_BASE_URL":          "https://api.anthropic.com/v1",
			"ANTHROPIC_AUTH_TOKEN":        "sk-ant-test-token",
			"ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-opus-4-5",
		},
	}
	csBytes, _ := json.Marshal(claudeSettings)
	_ = os.WriteFile(filepath.Join(claudeDir, "settings.json"), csBytes, 0o644)

	claudeLines := []string{
		`{"type":"custom-title","customTitle":"Claude Refactor Task","cwd":"` + workspaceDir + `/.claude/worktrees/wt1","gitBranch":"feat/claude","timestamp":"2026-10-17T10:00:00Z"}`,
		`{"type":"user","timestamp":"2026-10-17T10:00:01Z","message":{"content":"<system-reminder>ignore</system-reminder>"}}`,
		`{"type":"user","timestamp":"2026-10-17T10:00:02Z","message":{"content":[{"type":"text","text":"Please update app.go and write a todo plan"}]}}`,
		`{"type":"assistant","timestamp":"2026-10-17T10:00:05Z","message":{"model":"claude-sonnet-4-5","content":[{"type":"thinking","thinking":"I need to edit app.go and record a plan."},{"type":"text","text":"I will update app.go now."},{"type":"tool_use","id":"toolu_edit_1","name":"Edit","input":{"file_path":"app.go","old_string":"a","new_string":"b"}},{"type":"tool_use","id":"toolu_todo_1","name":"TodoWrite","input":{"items":[{"content":"Edit app.go","status":"completed"},{"content":"Run tests","status":"in_progress"}]}}]}}`,
		`{"type":"user","timestamp":"2026-10-17T10:00:06Z","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_edit_1","content":[{"type":"text","text":"Updated app.go successfully"}]},{"type":"tool_result","tool_use_id":"toolu_todo_1","content":"Todo list updated"}]}}`,
		`{"type":"assistant","timestamp":"2026-10-17T10:00:08Z","message":{"model":"claude-sonnet-4-5","content":[{"type":"thinking","thinking":"Now writing test file."},{"type":"tool_use","id":"toolu_write_1","name":"Write","input":{"filePath":"app_test.go","content":"package main"}},{"type":"text","text":"Created app_test.go."}]}}`,
	}
	_ = os.WriteFile(filepath.Join(claudeProjDir, "claude-sess-1.jsonl"), []byte(strings.Join(claudeLines, "\n")+"\n"), 0o644)

	// 2. OpenAI Codex fixture
	codexDir := filepath.Join(homeDir, ".codex")
	rolloutDir := filepath.Join(codexDir, "sessions", "2026", "10", "17")
	if err := os.MkdirAll(rolloutDir, 0o755); err != nil {
		t.Fatalf("mkdir codex rollout: %v", err)
	}
	_ = os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte("model = \"gpt-5-codex\"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(codexDir, "models_cache.json"), []byte(`{"client_version":"0.42.0","models":[{"slug":"gpt-5-codex"},{"slug":"o3"}]}`), 0o644)
	_ = os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte(`{"auth_mode":"api_key","OPENAI_API_KEY":"sk-codex-test-key","tokens":{"access_token":"acc-123","refresh_token":"ref-123","account_id":"acct-1"}}`), 0o644)

	rolloutPath := filepath.Join(rolloutDir, "rollout-codex-1.jsonl")
	codexLines := []string{
		`{"type":"session_meta","payload":{"id":"codex-sess-1","cwd":"` + workspaceDir + `"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":"# AGENTS.md instructions for project"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Implement codex feature in codex_feat.go"}]}}`,
		`{"type":"response_item","payload":{"type":"reasoning","summary":[{"text":"Planning codex feature"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","phase":"commentary","content":[{"text":"Checking files first"}]}}`,
		`{"type":"response_item","payload":{"type":"function_call","call_id":"call_cx_1","name":"write_file","arguments":"{\"filePath\":\"codex_feat.go\"}"}}`,
		`{"type":"response_item","payload":{"type":"function_call_output","call_id":"call_cx_1","output":"wrote codex_feat.go"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call","call_id":"call_cx_2","name":"exec_command","input":"go test ./..."}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call_cx_2","output":[{"text":"ok"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":"Codex feature complete."}}`,
	}
	_ = os.WriteFile(rolloutPath, []byte(strings.Join(codexLines, "\n")+"\n"), 0o644)

	codexDB := filepath.Join(codexDir, "state_5.sqlite")
	runSQLite(t, codexDB, `
CREATE TABLE threads (
	id TEXT PRIMARY KEY,
	rollout_path TEXT,
	created_at INTEGER,
	updated_at INTEGER,
	cwd TEXT,
	title TEXT,
	git_branch TEXT,
	git_sha TEXT,
	model TEXT,
	first_user_message TEXT,
	archived INTEGER
);
INSERT INTO threads VALUES (
	'codex-sess-1',
	`+sqlEscapeLiteral(rolloutPath)+`,
	1760695200,
	1760695800,
	`+sqlEscapeLiteral(workspaceDir)+`,
	'Codex Feature Task',
	'feat/codex',
	'1234567890abcdef',
	'gpt-5-codex',
	'Implement codex feature in codex_feat.go',
	0
);
`)

	// 3. Gemini / Antigravity fixture
	geminiDir := filepath.Join(homeDir, ".gemini")
	agyBrainLogs := filepath.Join(geminiDir, "antigravity", "brain", "gemini-sess-1", ".system_generated", "logs")
	if err := os.MkdirAll(agyBrainLogs, 0o755); err != nil {
		t.Fatalf("mkdir gemini brain logs: %v", err)
	}
	_ = os.WriteFile(filepath.Join(geminiDir, "settings.json"), []byte(`{"model":"gemini-2.5-pro","env":{"GEMINI_API_KEY":"AIza-gemini-test-key"}}`), 0o644)
	_ = os.WriteFile(filepath.Join(geminiDir, "jetski-standalone-oauth-token"), []byte("oauth-token"), 0o644)
	_ = os.WriteFile(filepath.Join(geminiDir, "antigravity", "brain", "gemini-sess-1", "task.md"), []byte("- [x] Inspect gemini.go\n- [/] Update transcript parser\n- [ ] Run verification\n"), 0o644)

	geminiLines := []string{
		"{\"type\":\"USER_INPUT\",\"created_at\":\"2026-10-17T10:00:00Z\",\"content\":\"User changed setting `Model Selection` to gemini-flash-latest.\\n<USER_REQUEST>\\nRefactor gemini.go and add tests\\n</USER_REQUEST>\"}",
		`{"type":"USER_INPUT","created_at":"2026-10-17T10:00:01Z","content":"<SYSTEM_MESSAGE>system check</SYSTEM_MESSAGE>"}`,
		`{"type":"PLANNER_RESPONSE","created_at":"2026-10-17T10:01:00Z","thinking":"Reading gemini.go first","content":"I will edit gemini_out.go","tool_calls":[{"name":"write_to_file","args":{"TargetFile":"\"gemini_out.go\"","CommandLine":"echo ok","toolSummary":"Write gemini_out.go"}}]}`,
		`{"type":"GENERIC","created_at":"2026-10-17T10:01:02Z","content":"Created gemini_out.go"}`,
		`{"type":"PLANNER_RESPONSE","created_at":"2026-10-17T10:02:00Z","thinking":"Verifying build","content":"All changes verified."}`,
	}
	_ = os.WriteFile(filepath.Join(agyBrainLogs, "transcript.jsonl"), []byte(strings.Join(geminiLines, "\n")+"\n"), 0o644)

	agyDB := filepath.Join(geminiDir, "antigravity", "conversation_summaries.db")
	runSQLite(t, agyDB, `
CREATE TABLE conversation_summaries (
	conversation_id TEXT PRIMARY KEY,
	title TEXT,
	preview TEXT,
	step_count INTEGER,
	last_modified_time TEXT,
	last_user_input_time TEXT,
	workspace_uris TEXT,
	status TEXT,
	nesting_depth INTEGER,
	killed INTEGER
);
INSERT INTO conversation_summaries VALUES (
	'gemini-sess-1',
	'Gemini Refactor',
	'Refactor gemini.go and add tests',
	4,
	'2026-10-17T10:10:00Z',
	'2026-10-17T10:00:00Z',
	'["file:///unrelated/path"]',
	'DONE',
	0,
	0
);
`)

	// 4. Grok CLI fixture
	grokDir := filepath.Join(homeDir, ".grok")
	grokSessDir := filepath.Join(grokDir, "sessions", url.QueryEscape(workspaceDir), "grok-sess-1")
	if err := os.MkdirAll(grokSessDir, 0o755); err != nil {
		t.Fatalf("mkdir grok session: %v", err)
	}
	_ = os.WriteFile(filepath.Join(grokDir, "auth.json"), []byte(`{"default":{"key":"xai-grok-test-key"}}`), 0o644)
	_ = os.WriteFile(filepath.Join(grokDir, "version.json"), []byte(`{"latest_version":"1.2.3"}`), 0o644)
	_ = os.WriteFile(filepath.Join(grokDir, "settings.json"), []byte(`{"model":"grok-4"}`), 0o644)
	_ = os.WriteFile(filepath.Join(grokDir, "models_cache.json"), []byte(`{"grok_version":"1.2.3","default_model":"grok-4","models":{"grok-4":{"info":{"id":"grok-4","hidden":false}},"grok-hidden":{"info":{"id":"grok-hidden","hidden":true}},"grok-3-mini":{}}}`), 0o644)

	grokSummary := `{
		"info": {"id": "grok-sess-1", "cwd": "` + workspaceDir + `"},
		"generated_title": "Grok Session Task",
		"session_summary": "Implemented grok bridge",
		"created_at": "2026-10-17T10:00:00Z",
		"updated_at": "2026-10-17T10:15:00Z",
		"current_model_id": "grok-4",
		"head_commit": "abcdef1234567890",
		"head_branch": "main",
		"last_turn_summary": "Completed grok changes",
		"last_recap": "Recap text"
	}`
	_ = os.WriteFile(filepath.Join(grokSessDir, "summary.json"), []byte(grokSummary), 0o644)

	grokHistoryLines := []string{
		`{"type":"user","synthetic_reason":"system_reminder","content":"ignore"}`,
		`{"type":"user","content":"<user_info>ignore</user_info>"}`,
		`{"type":"user","content":"Preface <user_query>Optimize grok_bridge.go</user_query> Suffix"}`,
		`{"type":"reasoning","summary":[{"text":"Analyzing grok_bridge.go"}]}`,
		`{"type":"assistant","model_id":"grok-4","content":[{"text":"Updating grok_bridge.go"}],"tool_calls":[{"id":"call_grok_1","name":"edit","arguments":"{\"filePath\":\"grok_bridge.go\"}"}]}`,
		`{"type":"tool_result","tool_call_id":"call_grok_1","content":[{"text":"Edited grok_bridge.go"}]}`,
		`{"type":"reasoning","summary":[{"text":"Double checking"}]}`,
		`{"type":"assistant","content":"Done optimizing grok_bridge.go."}`,
	}
	grokHistoryData := []byte(strings.Join(grokHistoryLines, "\n") + "\n")
	_ = os.WriteFile(filepath.Join(grokSessDir, "chat_history.jsonl"), grokHistoryData, 0o644)
	_ = os.WriteFile(filepath.Join(grokSessDir, "session.jsonl"), grokHistoryData, 0o644)
	_ = os.WriteFile(filepath.Join(grokSessDir, "plan.json"), []byte(`{"todos":{"step1":{"content":"Optimize bridge","status":"completed"},"step2":{"title":"Verify bridge","status":"pending"}}}`), 0o644)

	return homeDir
}

func TestSyntheticDiscoveryAndDetailAllSources(t *testing.T) {
	workspaceDir := t.TempDir()
	setupSyntheticCLIHome(t, workspaceDir)

	// 1. Claude Code
	claudeList, claudeStats := scanClaudeSessions(workspaceDir, "Refactor")
	if !claudeStats.Installed || claudeStats.SessionCount != 1 || len(claudeList) != 1 {
		t.Fatalf("unexpected claude scan: stats=%+v list=%+v", claudeStats, claudeList)
	}
	if claudeList[0].CWD != workspaceDir || !claudeList[0].MatchesCWD {
		t.Fatalf("expected worktree CWD normalized to %s, got %s", workspaceDir, claudeList[0].CWD)
	}
	claudeDetail, err := loadClaudeSessionDetail("claude-sess-1", workspaceDir, workspaceDir)
	if err != nil {
		t.Fatalf("loadClaudeSessionDetail failed: %v", err)
	}
	if len(claudeDetail.Turns) != 2 || len(claudeDetail.PlanSteps) != 2 {
		t.Fatalf("unexpected claude turns=%d plans=%d", len(claudeDetail.Turns), len(claudeDetail.PlanSteps))
	}
	if !reflect.DeepEqual(claudeDetail.ChangedFiles, []string{"app.go", "app_test.go"}) {
		t.Fatalf("unexpected claude changed files: %v", claudeDetail.ChangedFiles)
	}
	if claudeDetail.Turns[1].Tools[0].Output != "Updated app.go successfully" {
		t.Fatalf("expected tool_result output attached, got %q", claudeDetail.Turns[1].Tools[0].Output)
	}

	// 2. OpenAI Codex
	codexList, codexStats := scanCodexSessions(workspaceDir, "codex")
	if !codexStats.Installed || codexStats.SessionCount != 1 || len(codexList) != 1 {
		t.Fatalf("unexpected codex scan: stats=%+v list=%+v", codexStats, codexList)
	}
	codexDetail, err := loadCodexSessionDetail("codex-sess-1", workspaceDir, workspaceDir)
	if err != nil {
		t.Fatalf("loadCodexSessionDetail failed: %v", err)
	}
	if len(codexDetail.Turns) != 2 || !reflect.DeepEqual(codexDetail.ChangedFiles, []string{"codex_feat.go"}) {
		t.Fatalf("unexpected codex detail: turns=%d changed=%v", len(codexDetail.Turns), codexDetail.ChangedFiles)
	}
	if len(codexDetail.Turns[1].Tools) != 2 || codexDetail.Turns[1].Tools[0].Output != "wrote codex_feat.go" {
		t.Fatalf("unexpected codex tools: %+v", codexDetail.Turns[1].Tools)
	}

	// 3. Gemini / Antigravity
	geminiList, geminiStats := scanGeminiAntigravitySessions(workspaceDir, "Gemini")
	if !geminiStats.Installed || geminiStats.SessionCount != 1 || len(geminiList) != 1 {
		t.Fatalf("unexpected gemini scan: stats=%+v list=%+v", geminiStats, geminiList)
	}
	geminiDetail, err := loadGeminiAntigravitySessionDetail("gemini-sess-1", workspaceDir, workspaceDir)
	if err != nil {
		t.Fatalf("loadGeminiAntigravitySessionDetail failed: %v", err)
	}
	if geminiDetail.Summary.Model != "gemini-flash-latest" || len(geminiDetail.Turns) != 2 || len(geminiDetail.PlanSteps) != 3 {
		t.Fatalf("unexpected gemini detail: model=%s turns=%d plans=%d", geminiDetail.Summary.Model, len(geminiDetail.Turns), len(geminiDetail.PlanSteps))
	}
	if !reflect.DeepEqual(geminiDetail.ChangedFiles, []string{"gemini_out.go"}) || geminiDetail.Turns[1].Tools[0].Output != "Created gemini_out.go" {
		t.Fatalf("unexpected gemini tools/files: files=%v tools=%+v", geminiDetail.ChangedFiles, geminiDetail.Turns[1].Tools)
	}

	// 4. Grok CLI
	grokList, grokStats := scanGrokSessions(workspaceDir, "Grok")
	if !grokStats.Installed || grokStats.SessionCount != 1 || len(grokList) != 1 {
		t.Fatalf("unexpected grok scan: stats=%+v list=%+v", grokStats, grokList)
	}
	grokDetail, err := loadGrokSessionDetail("grok-sess-1", workspaceDir, workspaceDir)
	if err != nil {
		t.Fatalf("loadGrokSessionDetail failed: %v", err)
	}
	if len(grokDetail.Turns) != 2 || len(grokDetail.PlanSteps) != 2 {
		t.Fatalf("unexpected grok detail: turns=%d plans=%d", len(grokDetail.Turns), len(grokDetail.PlanSteps))
	}
	if grokDetail.Turns[0].Text != "Optimize grok_bridge.go" || !reflect.DeepEqual(grokDetail.ChangedFiles, []string{"grok_bridge.go"}) {
		t.Fatalf("unexpected grok turn0=%q files=%v", grokDetail.Turns[0].Text, grokDetail.ChangedFiles)
	}
}

func TestHelperFunctions(t *testing.T) {
	// 1. normalizeExternalToolName
	toolCases := map[string]string{
		"read_file":            "read",
		"view_file":            "read",
		"replace_file_content": "edit",
		"apply_patch":          "edit",
		"write_to_file":        "write",
		"exec_command":         "bash",
		"run_command":          "bash",
		"grep_search":          "grep",
		"find_by_name":         "glob",
		"":                     "bash",
		"Custom_Tool":          "custom_tool",
	}
	for raw, want := range toolCases {
		if got := normalizeExternalToolName(raw); got != want {
			t.Errorf("normalizeExternalToolName(%q) = %q, want %q", raw, got, want)
		}
	}

	// 2. extractChangedFilesFromTurns
	turns := []NormalizedTurn{
		{
			Role: "assistant",
			Tools: []NormalizedToolCall{
				{Tool: "read", Input: map[string]interface{}{"filePath": "ignored.go"}},
				{Tool: "edit", Input: map[string]interface{}{"file_path": "a.go", "path": "b.go"}},
				{Tool: "write", Input: map[string]interface{}{"target_file": "a.go", "file": "c.go"}},
			},
		},
	}
	if got := extractChangedFilesFromTurns(turns); !reflect.DeepEqual(got, []string{"a.go", "b.go", "c.go"}) {
		t.Fatalf("extractChangedFilesFromTurns = %v", got)
	}

	// 3. cleanUserQueryTags
	if got := cleanUserQueryTags("  hello <user_query>  fix bug  </user_query> world "); got != "fix bug" {
		t.Fatalf("cleanUserQueryTags = %q", got)
	}
	if got := cleanUserQueryTags("plain prompt"); got != "plain prompt" {
		t.Fatalf("cleanUserQueryTags plain = %q", got)
	}

	// 4. sanitizeToolCallID
	if got := sanitizeToolCallID("", 7); got != "call_handoff_7" {
		t.Fatalf("sanitizeToolCallID empty = %q", got)
	}
	longID := "call:123/456@789#abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	sanitized := sanitizeToolCallID(longID, 1)
	if len(sanitized) != 48 || !strings.HasPrefix(sanitized, "call_123_456_789_") {
		t.Fatalf("sanitizeToolCallID long = %q (len=%d)", sanitized, len(sanitized))
	}

	// 5. sqlEscapeLiteral
	if got := sqlEscapeLiteral("it's a 'test'"); got != "'it''s a ''test'''" {
		t.Fatalf("sqlEscapeLiteral = %q", got)
	}

	// 6. extractClaudeAssistantContent
	txt, th, calls, plans := extractClaudeAssistantContent("simple text")
	if txt != "simple text" || th != "" || len(calls) != 0 || len(plans) != 0 {
		t.Fatalf("extractClaudeAssistantContent string failed: %q", txt)
	}
	txt, th, calls, plans = extractClaudeAssistantContent([]interface{}{
		map[string]interface{}{"type": "thinking", "thinking": "deep thought"},
		map[string]interface{}{"type": "text", "text": "hello"},
		map[string]interface{}{
			"type": "tool_use",
			"id":   "tu_1",
			"name": "TodoWrite",
			"input": map[string]interface{}{
				"items": []interface{}{
					map[string]interface{}{"content": "step 1", "status": "completed"},
				},
			},
		},
	})
	if txt != "hello" || th != "deep thought" || len(calls) != 1 || len(plans) != 1 || plans[0].Title != "step 1" {
		t.Fatalf("extractClaudeAssistantContent array failed: txt=%q th=%q calls=%v plans=%v", txt, th, calls, plans)
	}

	// 7. comparableFolderPath & sameOrSubPath
	if got := comparableFolderPath(" /tmp/a/../b/ "); got != "/tmp/b" {
		t.Fatalf("comparableFolderPath = %q", got)
	}
	if !sameOrSubPath("/tmp/work/sub", "/tmp/work") || sameOrSubPath(".", "/tmp/work") {
		t.Fatalf("sameOrSubPath failed")
	}

	// 8. truncateRunes
	if got := truncateRunes("你好世界Hello", 4); got != "你好世界" {
		t.Fatalf("truncateRunes = %q", got)
	}
	if got := truncateRunes("short", 10); got != "short" {
		t.Fatalf("truncateRunes short = %q", got)
	}
}
