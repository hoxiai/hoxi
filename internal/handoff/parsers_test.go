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

func TestImportedSessionTitlePrefixesMatchActualLabels(t *testing.T) {
	clause := SQLNotImportedTitleClause("title")
	for _, title := range []string{"[OpenAI Codex] task", "[Antigravity (Gemini)] task"} {
		matched := false
		for _, pattern := range ImportedSessionTitlePrefixes() {
			prefix := strings.TrimSuffix(pattern, "%")
			if strings.HasPrefix(title, prefix) {
				matched = strings.Contains(clause, "title NOT LIKE '"+pattern+"'")
				break
			}
		}
		if !matched {
			t.Errorf("imported title %q has no matching exclusion prefix in %q", title, clause)
		}
	}
}

func setupSyntheticCLIHome(t *testing.T, workspaceDir string) string {
	t.Helper()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("APPDATA", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOXI_ENGINE_DATA_DIR", "")
	t.Setenv("OPENCODE_DATA_DIR", "")
	t.Setenv("OPENCODE_EXTERNAL_DB", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("GEMINI_HOME", filepath.Join(homeDir, ".gemini"))
	t.Setenv("ANTIGRAVITY_HOME", "")
	t.Setenv("GROK_HOME", "")
	t.Setenv("CODEBUDDY_HOME", filepath.Join(homeDir, ".codebuddy"))
	t.Setenv("CODEBUDDY_DATA_DIR", "")
	t.Setenv("TRAE_HOME", "")
	t.Setenv("TRAE_DATA_DIR", "")
	t.Setenv("QODER_HOME", filepath.Join(homeDir, ".qoder"))
	t.Setenv("QODER_DATA_DIR", "")
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
			"ANTHROPIC_BASE_URL":           "https://api.anthropic.com/v1",
			"ANTHROPIC_AUTH_TOKEN":         "sk-ant-test-token",
			"ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-opus-4-5",
		},
	}
	csBytes, _ := json.Marshal(claudeSettings)
	_ = os.WriteFile(filepath.Join(claudeDir, "settings.json"), csBytes, 0o644)

	claudeLines := []string{
		`{"type":"custom-title","customTitle":"Claude Refactor Task","cwd":"` + workspaceDir + `/.claude/worktrees/wt1","gitBranch":"feat/claude","timestamp":"2026-10-17T10:00:00Z"}`,
		`{"type":"user","timestamp":"2026-10-17T10:00:01Z","message":{"content":"<system-reminder>ignore</system-reminder>"}}`,
		`{"type":"user","timestamp":"2026-10-17T10:00:02Z","message":{"content":[{"type":"text","text":"Please update app.go and write a todo plan"}]}}`,
		`{"type":"assistant","timestamp":"2026-10-17T10:00:05Z","message":{"model":"claude-sonnet-4-5","usage":{"input_tokens":1200,"output_tokens":80,"cache_read_input_tokens":400,"cache_creation_input_tokens":50},"content":[{"type":"thinking","thinking":"I need to edit app.go and record a plan."},{"type":"text","text":"I will update app.go now."},{"type":"tool_use","id":"toolu_edit_1","name":"Edit","input":{"file_path":"app.go","old_string":"a","new_string":"b"}},{"type":"tool_use","id":"toolu_todo_1","name":"TodoWrite","input":{"items":[{"content":"Edit app.go","status":"completed"},{"content":"Run tests","status":"in_progress"}]}}]}}`,
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
	_ = os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte("model = \"o3\"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(codexDir, "models_cache.json"), []byte(`{"client_version":"0.42.0","models":[{"slug":"gpt-5-codex"},{"slug":"o3"}]}`), 0o644)
	_ = os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte(`{"auth_mode":"api_key","OPENAI_API_KEY":"sk-codex-test-key","tokens":{"access_token":"acc-123","refresh_token":"ref-123","account_id":"acct-1"}}`), 0o644)

	rolloutPath := filepath.Join(rolloutDir, "rollout-codex-1.jsonl")
	codexLines := []string{
		`{"type":"session_meta","payload":{"id":"codex-sess-1","cwd":"` + workspaceDir + `"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":"# AGENTS.md instructions for project"}}`,
		`{"type":"response_item","timestamp":"2026-10-17T10:00:02Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Implement codex feature in codex_feat.go"}]}}`,
		`{"type":"response_item","payload":{"type":"reasoning","summary":[{"text":"Planning codex feature"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","phase":"commentary","content":[{"text":"Checking files first"}]}}`,
		`{"type":"response_item","payload":{"type":"function_call","call_id":"call_cx_1","name":"write_file","arguments":"{\"filePath\":\"codex_feat.go\"}"}}`,
		`{"type":"response_item","payload":{"type":"function_call_output","call_id":"call_cx_1","output":"wrote codex_feat.go"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call","call_id":"call_cx_2","name":"exec_command","input":"go test ./..."}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call_cx_2","output":[{"text":"ok"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":"Codex feature complete."}}`,
		`{"type":"token_usage_record","payload":{"turn_id":"turn-1","usage":{"input_tokens":900,"cached_input_tokens":300,"output_tokens":40,"reasoning_output_tokens":10,"cache_write_input_tokens":20},"turn_token_usage":{"input_tokens":900,"cached_input_tokens":300,"output_tokens":40,"reasoning_output_tokens":10,"cache_write_input_tokens":20},"thread_token_usage":{"input_tokens":900,"cached_input_tokens":300,"output_tokens":40,"reasoning_output_tokens":10,"cache_write_input_tokens":20}}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1800,"cached_input_tokens":600,"output_tokens":80,"reasoning_output_tokens":20},"last_token_usage":{"input_tokens":900,"cached_input_tokens":300,"output_tokens":40,"reasoning_output_tokens":10,"cache_write_input_tokens":20}}}}`,
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
	`+sqlEscapeLiteral(`["file://`+filepath.ToSlash(workspaceDir)+`"]`)+`,
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

	// 5. CodeBuddy fixture
	cbExtHistDir := filepath.Join(homeDir, "Library", "Application Support", "CodeBuddyExtension", "Data", "uid1", "CodeBuddyIDE", "uid1", "history", md5HexString(workspaceDir))
	cbSessMsgsDir := filepath.Join(cbExtHistDir, "cb-sess-1", "messages")
	if err := os.MkdirAll(cbSessMsgsDir, 0o755); err != nil {
		t.Fatalf("mkdir codebuddy msgs: %v", err)
	}
	cbCopilotDir := filepath.Join(homeDir, "Library", "Application Support", "CodeBuddy CN", "User", "globalStorage", "tencent-cloud.coding-copilot")
	_ = os.MkdirAll(filepath.Join(cbCopilotDir, "todos"), 0o755)
	_ = os.MkdirAll(filepath.Join(cbCopilotDir, "brain", "cb-sess-1"), 0o755)
	_ = os.WriteFile(filepath.Join(cbCopilotDir, "todos", "cb-sess-1.json"), []byte(`{"conversationId":"cb-sess-1","todos":[{"id":"1","status":"completed","content":"Analyze CodeBuddy session"},{"id":"2","status":"in_progress","content":"Write cb_out.go"}]}`), 0o644)
	_ = os.WriteFile(filepath.Join(cbCopilotDir, "brain", "cb-sess-1", "audit.md"), []byte("# CodeBuddy Audit Recap"), 0o644)

	cbDB := filepath.Join(homeDir, "Library", "Application Support", "CodeBuddy CN", "codebuddy-sessions.vscdb")
	runSQLite(t, cbDB, `
CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value BLOB);
INSERT INTO ItemTable VALUES (
	'session:cb-sess-1',
	`+sqlEscapeLiteral(`{"conversationId":"cb-sess-1","cwd":"`+workspaceDir+`","title":"CodeBuddy Refactor","createdAt":1760695000000,"updatedAt":1760695900000}`)+`
);
`)
	_ = os.WriteFile(filepath.Join(cbExtHistDir, "index.json"), []byte(`{"conversations":[{"id":"cb-sess-1","name":"CodeBuddy Refactor","createdAt":"2026-10-17T10:00:00Z","lastMessageAt":"2026-10-17T10:15:00Z","selectedModelId":"step-5-preview"}]}`), 0o644)
	_ = os.WriteFile(filepath.Join(cbExtHistDir, "cb-sess-1", "index.json"), []byte(`{"messages":[{"id":"m1","role":"user"},{"id":"m2","role":"assistant"},{"id":"m3","role":"tool"}]}`), 0o644)

	cbMsg1 := map[string]interface{}{
		"id":        "m1",
		"role":      "user",
		"message":   `{"role":"user","content":[{"type":"text","text":"<user_info>\nWorkspace Folder: ` + workspaceDir + `\n</user_info>\nRefactor cb_out.go"}]}`,
		"extra":     `{"modelId":"step-5-preview","sourceContentBlocks":[{"type":"text","text":"Refactor cb_out.go"}]}`,
		"createdAt": "2026-10-17T10:00:01Z",
	}
	cbMsg2 := map[string]interface{}{
		"id":        "m2",
		"role":      "assistant",
		"message":   `{"role":"assistant","content":[{"type":"reasoning","text":"Planning cb_out.go"},{"type":"text","text":"Writing cb_out.go now."},{"type":"tool-call","toolCallId":"cb_call_1","toolName":"write_to_file","args":{"filePath":"cb_out.go","content":"package cb"}}]}`,
		"extra":     `{"modelId":"step-5-preview"}`,
		"createdAt": "2026-10-17T10:00:05Z",
	}
	cbMsg3 := map[string]interface{}{
		"id":        "m3",
		"role":      "tool",
		"message":   `{"role":"tool","content":[{"type":"tool-result","toolCallId":"cb_call_1","toolName":"write_to_file","result":{"status":"success","result":{"stdout":"wrote cb_out.go"}}}]}`,
		"extra":     `{"modelId":"step-5-preview"}`,
		"createdAt": "2026-10-17T10:00:06Z",
	}
	for name, doc := range map[string]interface{}{"m1.json": cbMsg1, "m2.json": cbMsg2, "m3.json": cbMsg3} {
		b, _ := json.Marshal(doc)
		_ = os.WriteFile(filepath.Join(cbSessMsgsDir, name), b, 0o644)
	}

	// 6. Trae fixture
	traeProjDir := filepath.Join(homeDir, ".trae-cn", "memory", "projects", encodeClaudeProjectHint(workspaceDir)+"--p2-abcdef1234", "20261017")
	if err := os.MkdirAll(traeProjDir, 0o755); err != nil {
		t.Fatalf("mkdir trae proj: %v", err)
	}
	_ = os.WriteFile(filepath.Join(filepath.Dir(traeProjDir), "project_memory.md"), []byte("# Trae Project Memory\n- Go + SolidJS architecture"), 0o644)
	_ = os.WriteFile(filepath.Join(traeProjDir, "topics.md"), []byte("[session_id: trae-sess-1 | topic_summary_time: 2026-10-17 10:05:00]Completed Trae architecture review and fixed soft-delete bug.\n"), 0o644)
	traeMemLine := `{"intent":"Refactor Trae module and audit soft-delete","actions":["Scan codebase for GORM Table() calls","Verify soft-delete filter"],"outcome":"Fixed soft-delete bug in Trae module","learned":["Avoid raw Table() without deleted_at filter"],"message_summary_time":"2026-10-17 10:06:00","message_id":"msg_trae_1","compact_summary_meta":{"created_at_ms":1760695560000}}`
	_ = os.WriteFile(filepath.Join(traeProjDir, "session_memory_trae-sess-1.jsonl"), []byte(traeMemLine+"\n"), 0o644)

	traeVSCDB := filepath.Join(homeDir, "Library", "Application Support", "Trae CN", "User", "globalStorage", "state.vscdb")
	runSQLite(t, traeVSCDB, `
CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value BLOB);
INSERT INTO ItemTable VALUES (
	'u1:AI.agent.model.session_selected_model',
	'{"trae-sess-1":{"solo_agent":{"modelId":"solo_agent_1__glm-5.3-flash_null","mode":0}}}'
);
`)

	// 7. Qoder fixture
	qoderTransDir := filepath.Join(homeDir, ".qoder", "projects", encodeClaudeProjectHint(workspaceDir), "transcript")
	if err := os.MkdirAll(qoderTransDir, 0o755); err != nil {
		t.Fatalf("mkdir qoder transcript: %v", err)
	}
	qoderLines := []string{
		`{"type":"session_meta","sessionId":"qoder-sess-1","timestamp":"2026-10-17T10:00:00Z","cwd":"` + workspaceDir + `"}`,
		`{"type":"user","sessionId":"qoder-sess-1","timestamp":"2026-10-17T10:00:02Z","cwd":"` + workspaceDir + `","message":{"role":"user","content":"Refactor qoder_out.go"}}`,
		`{"type":"assistant","sessionId":"qoder-sess-1","timestamp":"2026-10-17T10:00:05Z","cwd":"` + workspaceDir + `","message":{"role":"assistant","content":[{"type":"thinking","thinking":"Inspecting qoder_out.go"},{"type":"text","text":"Updating qoder_out.go."},{"type":"tool_use","id":"toolu_qoder_1","name":"Edit","input":{"file_path":"qoder_out.go"}}]}}`,
		`{"type":"user","sessionId":"qoder-sess-1","timestamp":"2026-10-17T10:00:06Z","cwd":"` + workspaceDir + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_qoder_1","content":"Updated qoder_out.go"}]}}`,
	}
	_ = os.WriteFile(filepath.Join(qoderTransDir, "qoder-sess-1.jsonl"), []byte(strings.Join(qoderLines, "\n")+"\n"), 0o644)

	qoderLocalDB := filepath.Join(homeDir, "Library", "Application Support", "Qoder", "SharedClientCache", "cache", "db", "local.db")
	runSQLite(t, qoderLocalDB, `
CREATE TABLE chat_session (
	session_id TEXT PRIMARY KEY,
	session_title TEXT,
	project_uri TEXT,
	project_name TEXT,
	gmt_create INTEGER,
	gmt_modified INTEGER,
	preferred_model_info TEXT,
	parent_session_id TEXT
);
INSERT INTO chat_session VALUES (
	'qoder-sess-1',
	'Qoder Refactor Task',
	`+sqlEscapeLiteral(workspaceDir)+`,
	'work',
	1760695100000,
	1760695950000,
	'{"preferred_model":"qmodel_38max"}',
	''
);
CREATE TABLE agent_memory (
	id TEXT PRIMARY KEY,
	title TEXT,
	content TEXT,
	session_id TEXT,
	gmt_modified INTEGER
);
	INSERT INTO agent_memory VALUES ('m1', 'Qoder Tech Stack', 'Go + SolidJS', 'qoder-sess-1', 1760695950000);
`)

	// 8. External OpenCode CLI fixture (~/.local/share/opencode/opencode.db)
	t.Setenv("OPENCODE_EXTERNAL_DB", "")
	t.Setenv("OPENCODE_DATA_DIR", "")
	extOpenCodeDB := filepath.Join(homeDir, ".local", "share", "opencode", "opencode.db")
	runSQLite(t, extOpenCodeDB, `
CREATE TABLE session (
	id TEXT PRIMARY KEY,
	parent_id TEXT,
	directory TEXT,
	title TEXT,
	time_created INTEGER,
	time_updated INTEGER,
	model TEXT,
	metadata TEXT
);
CREATE TABLE message (
	id TEXT PRIMARY KEY,
	session_id TEXT,
	time_created INTEGER,
	time_updated INTEGER,
	data TEXT
);
CREATE TABLE part (
	id TEXT PRIMARY KEY,
	message_id TEXT,
	session_id TEXT,
	time_created INTEGER,
	time_updated INTEGER,
	data TEXT
);
INSERT INTO session VALUES (
	'opencode-sess-1',
	NULL,
	`+sqlEscapeLiteral(workspaceDir)+`,
	'OpenCode Legacy CLI Task',
	1760695000000,
	1760695980000,
	'{"id":"claude-sonnet-4-5","providerID":"anthropic"}',
	NULL
);
INSERT INTO message VALUES (
	'msg_oc_u1',
	'opencode-sess-1',
	1760695001000,
	1760695001000,
	'{"role":"user","model":{"providerID":"anthropic","modelID":"claude-sonnet-4-5"}}'
);
INSERT INTO part VALUES (
	'prt_oc_u1',
	'msg_oc_u1',
	'opencode-sess-1',
	1760695001000,
	1760695001000,
	'{"type":"text","text":"Audit opencode_out.go"}'
);
INSERT INTO message VALUES (
	'msg_oc_a1',
	'opencode-sess-1',
	1760695005000,
	1760695005000,
	'{"role":"assistant","providerID":"anthropic","modelID":"claude-sonnet-4-5"}'
);
INSERT INTO part VALUES (
	'prt_oc_a1_r',
	'msg_oc_a1',
	'opencode-sess-1',
	1760695005000,
	1760695005000,
	'{"type":"reasoning","text":"Checking opencode_out.go"}'
);
INSERT INTO part VALUES (
	'prt_oc_a1_t',
	'msg_oc_a1',
	'opencode-sess-1',
	1760695006000,
	1760695006000,
	'{"type":"tool","tool":"edit","callID":"call_oc_1","state":{"status":"completed","title":"Edit opencode_out.go","input":{"filePath":"opencode_out.go"},"output":"edited opencode_out.go"}}'
);
INSERT INTO part VALUES (
	'prt_oc_a1_txt',
	'msg_oc_a1',
	'opencode-sess-1',
	1760695007000,
	1760695007000,
	'{"type":"text","text":"Completed audit of opencode_out.go."}'
);
`)

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
	if claudeDetail.Turns[1].Usage.Input != 1200 || claudeDetail.Turns[1].Usage.Output != 80 || claudeDetail.Turns[1].Usage.CacheRead != 400 || claudeDetail.Turns[1].Usage.CacheWrite != 50 {
		t.Fatalf("expected claude usage preserved, got %+v", claudeDetail.Turns[1].Usage)
	}

	// 2. OpenAI Codex
	codexList, codexStats := scanCodexSessions(workspaceDir, "feat/codex")
	if !codexStats.Installed || codexStats.SessionCount != 1 || len(codexList) != 1 {
		t.Fatalf("unexpected codex scan: stats=%+v list=%+v", codexStats, codexList)
	}
	if codexList[0].Model != "gpt-5-codex" || codexList[0].GitBranch != "feat/codex" || codexList[0].GitCommit != "12345678" || codexList[0].Summary != "Implement codex feature in codex_feat.go" {
		t.Fatalf("unexpected codex summary fields: %+v", codexList[0])
	}
	codexDetail, err := loadCodexSessionDetail("codex-sess-1", workspaceDir, workspaceDir)
	if err != nil {
		t.Fatalf("loadCodexSessionDetail failed: %v", err)
	}
	if codexDetail.Summary.Model != "gpt-5-codex" || codexDetail.Summary.GitBranch != "feat/codex" || codexDetail.Summary.GitCommit != "12345678" {
		t.Fatalf("unexpected codex detail summary fields: %+v", codexDetail.Summary)
	}
	if len(codexDetail.Turns) != 2 || !reflect.DeepEqual(codexDetail.ChangedFiles, []string{"codex_feat.go"}) {
		t.Fatalf("unexpected codex detail: turns=%d changed=%v", len(codexDetail.Turns), codexDetail.ChangedFiles)
	}
	if codexDetail.Turns[0].Timestamp != parseTimeMs("2026-10-17T10:00:02Z") {
		t.Fatalf("expected Codex source timestamp preserved, got %d", codexDetail.Turns[0].Timestamp)
	}
	if len(codexDetail.Turns[1].Tools) != 2 || codexDetail.Turns[1].Tools[0].Output != "wrote codex_feat.go" {
		t.Fatalf("unexpected codex tools: %+v", codexDetail.Turns[1].Tools)
	}
	if codexDetail.Turns[1].Usage.Input != 580 || codexDetail.Turns[1].Usage.Output != 30 || codexDetail.Turns[1].Usage.Reasoning != 10 || codexDetail.Turns[1].Usage.CacheRead != 300 || codexDetail.Turns[1].Usage.CacheWrite != 20 {
		t.Fatalf("expected codex token_count attached, got %+v", codexDetail.Turns[1].Usage)
	}

	// 3. Gemini / Antigravity
	geminiList, geminiStats := scanGeminiAntigravitySessions(workspaceDir, "Refactor")
	if !geminiStats.Installed || geminiStats.SessionCount != 1 || len(geminiList) != 1 {
		t.Fatalf("unexpected gemini scan: stats=%+v list=%+v", geminiStats, geminiList)
	}
	if geminiList[0].Title != "Gemini Refactor" || geminiList[0].Summary != "Refactor gemini.go and add tests" || geminiList[0].CWD != workspaceDir || !geminiList[0].MatchesCWD || geminiList[0].UpdatedAt == 0 {
		t.Fatalf("unexpected gemini summary fields: %+v", geminiList[0])
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

	// 5. CodeBuddy
	cbList, cbStats := scanCodeBuddySessions(workspaceDir, "CodeBuddy")
	if !cbStats.Installed || cbStats.SessionCount != 1 || len(cbList) != 1 || !cbList[0].MatchesCWD {
		t.Fatalf("unexpected codebuddy scan: stats=%+v list=%+v", cbStats, cbList)
	}
	cbDetail, err := loadCodeBuddySessionDetail("cb-sess-1", workspaceDir, workspaceDir)
	if err != nil {
		t.Fatalf("loadCodeBuddySessionDetail failed: %v", err)
	}
	if cbDetail.Summary.Model != "step-5-preview" || len(cbDetail.Turns) != 2 || len(cbDetail.PlanSteps) != 2 {
		t.Fatalf("unexpected codebuddy detail: model=%s turns=%d plans=%d", cbDetail.Summary.Model, len(cbDetail.Turns), len(cbDetail.PlanSteps))
	}
	if !reflect.DeepEqual(cbDetail.ChangedFiles, []string{"cb_out.go"}) || cbDetail.Turns[1].Tools[0].Output != "wrote cb_out.go" {
		t.Fatalf("unexpected codebuddy files/tools: files=%v tools=%+v", cbDetail.ChangedFiles, cbDetail.Turns[1].Tools)
	}

	// 6. Trae
	traeList, traeStats := scanTraeSessions(workspaceDir, "soft-delete")
	if !traeStats.Installed || traeStats.SessionCount != 1 || len(traeList) != 1 || !traeList[0].MatchesCWD {
		t.Fatalf("unexpected trae scan: stats=%+v list=%+v", traeStats, traeList)
	}
	traeDetail, err := loadTraeSessionDetail("trae-sess-1", workspaceDir, workspaceDir)
	if err != nil {
		t.Fatalf("loadTraeSessionDetail failed: %v", err)
	}
	if traeDetail.Summary.Model != "glm-5.3-flash" || len(traeDetail.Turns) != 2 || len(traeDetail.PlanSteps) != 2 {
		t.Fatalf("unexpected trae detail: model=%s turns=%d plans=%d", traeDetail.Summary.Model, len(traeDetail.Turns), len(traeDetail.PlanSteps))
	}

	// 7. Qoder
	qoderList, qoderStats := scanQoderSessions(workspaceDir, "Qoder")
	if !qoderStats.Installed || qoderStats.SessionCount != 1 || len(qoderList) != 1 || !qoderList[0].MatchesCWD {
		t.Fatalf("unexpected qoder scan: stats=%+v list=%+v", qoderStats, qoderList)
	}
	qoderDetail, err := loadQoderSessionDetail("qoder-sess-1", workspaceDir, workspaceDir)
	if err != nil {
		t.Fatalf("loadQoderSessionDetail failed: %v", err)
	}
	if qoderDetail.Summary.Model != "qmodel_38max" || len(qoderDetail.Turns) != 2 || !reflect.DeepEqual(qoderDetail.ChangedFiles, []string{"qoder_out.go"}) {
		t.Fatalf("unexpected qoder detail: model=%s turns=%d files=%v", qoderDetail.Summary.Model, len(qoderDetail.Turns), qoderDetail.ChangedFiles)
	}
	if !strings.Contains(qoderDetail.LastRecap, "Qoder Tech Stack: Go + SolidJS") {
		t.Fatalf("expected qoder agent_memory in LastRecap, got %q", qoderDetail.LastRecap)
	}

	// 8. External OpenCode CLI (opencode.db)
	ocList, ocStats := scanOpenCodeSessions(workspaceDir, "Legacy")
	if !ocStats.Installed || ocStats.SessionCount != 1 || len(ocList) != 1 || !ocList[0].MatchesCWD {
		t.Fatalf("unexpected opencode scan: stats=%+v list=%+v", ocStats, ocList)
	}
	ocDetail, err := loadOpenCodeSessionDetail("opencode-sess-1", workspaceDir, workspaceDir)
	if err != nil {
		t.Fatalf("loadOpenCodeSessionDetail failed: %v", err)
	}
	if ocDetail.Summary.Model != "anthropic/claude-sonnet-4-5" || len(ocDetail.Turns) != 2 || !reflect.DeepEqual(ocDetail.ChangedFiles, []string{"opencode_out.go"}) {
		t.Fatalf("unexpected opencode detail: model=%s turns=%d files=%v", ocDetail.Summary.Model, len(ocDetail.Turns), ocDetail.ChangedFiles)
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

	// 9. parseAgyModelsOutput & normalizeAntigravityModelID
	slugs, dMap, rMap := parseAgyModelsOutput("\r⠋ Fetching available models...\rgemini-4-argon-high     Gemini 4 Argon (High)\nclaude-opus-5-5-medium  Claude Opus 5.5 (Medium)\n")
	if len(slugs) != 2 || dMap["gemini-4-argon-high"] != "Gemini 4 Argon (High)" || rMap["claude opus 5.5 (medium)"] != "claude-opus-5-5-medium" {
		t.Fatalf("parseAgyModelsOutput unexpected: slugs=%v dMap=%v rMap=%v", slugs, dMap, rMap)
	}
	if got := normalizeAntigravityModelID("Gemini 4 Argon (High)"); got != "gemini-4-argon-high" {
		t.Fatalf("normalizeAntigravityModelID = %q, want gemini-4-argon-high", got)
	}
	if got := normalizeAntigravityModelID("Claude Opus 5.5 (Medium)"); got != "claude-opus-5-5-medium" {
		t.Fatalf("normalizeAntigravityModelID = %q, want claude-opus-5-5-medium", got)
	}
	_, extractedModel := extractAntigravityUserPrompt("User changed setting `Model Selection` to Gemini 4 Argon (Medium).\n<USER_REQUEST>\nhi\n</USER_REQUEST>")
	if extractedModel != "gemini-4-argon-medium" {
		t.Fatalf("extractAntigravityUserPrompt model = %q, want gemini-4-argon-medium", extractedModel)
	}
}
