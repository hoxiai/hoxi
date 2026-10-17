package provider

import "testing"

func TestParseOpenAIModelsPayloadAndExtractAPIError(t *testing.T) {
	raw := []byte(`{"data":[{"id":"gpt-4o","name":"GPT-4o"},{"id":"claude-3-7-sonnet"},{"id":"gpt-4o"}]}`)
	models := parseOpenAIModelsPayload(raw)
	if len(models) != 2 {
		t.Fatalf("expected 2 deduplicated models, got %d", len(models))
	}
	if models[0].ID != "claude-3-7-sonnet" || models[1].ID != "gpt-4o" {
		t.Errorf("unexpected sorted models: %+v", models)
	}

	errMsg := extractAPIErrorMessage(401, []byte(`{"error":{"message":"Invalid API key provided"}}`))
	if errMsg != "HTTP 401: Invalid API key provided" {
		t.Errorf("unexpected error message: %q", errMsg)
	}
}

func TestCleanAndFallbackSummarizeTitle(t *testing.T) {
	rawZh := "<think>思考中...</think>\n标题：“修复网关 CORS 与单元测试”."
	if got := cleanSummarizedTitle(rawZh, "zh-CN"); got != "修复网关 CORS 与单元测试" {
		t.Errorf("cleanSummarizedTitle zh-CN = %q, want %q", got, "修复网关 CORS 与单元测试")
	}

	rawEn := `Title: "Optimize SSE Streaming & Worktree Isolation".`
	if got := cleanSummarizedTitle(rawEn, "en-US"); got != "Optimize SSE Streaming & Worktree Isolation" {
		t.Errorf("cleanSummarizedTitle en-US = %q, want %q", got, "Optimize SSE Streaming & Worktree Isolation")
	}

	fb := fallbackSummarizeTitle("请帮我排查 desktop_gateway.go 里的 CORS 配置问题。顺便加上单元测试。", "zh-CN")
	if fb != "请帮我排查 desktop_gateway." {
		if fb == "" {
			t.Fatalf("expected non-empty fallback title")
		}
	}
}

func TestEvaluateStaticCodeRulesAndParseObserverAudit(t *testing.T) {
	diffs := []auditDiffFile{
		{
			File:      "web/src/App.tsx",
			Before:    "const a = 1;\n",
			After:     "const a = 2;\n// TODO: implement rest of logic\n",
			Additions: 2,
			Deletions: 1,
		},
	}

	deduction, issues := evaluateStaticCodeRules(diffs, 1, "TS2322 type mismatch", "zh-CN")
	if deduction < 30 {
		t.Fatalf("expected deduction >= 30 for LSP error + lazy TODO placeholder, got %d", deduction)
	}
	if len(issues) < 2 {
		t.Fatalf("expected at least 2 static issues, got %v", issues)
	}

	score, summary, parsedIssues := parseObserverAuditJSON("```json\n{\"score\": 68, \"pass\": false, \"summary\": \"Missing error handling\", \"issues\": [\"Unchecked null pointer\"]}\n```")
	if score != 68 || summary != "Missing error handling" || len(parsedIssues) != 1 {
		t.Fatalf("unexpected parsed observer JSON: score=%d summary=%q issues=%v", score, summary, parsedIssues)
	}
}
