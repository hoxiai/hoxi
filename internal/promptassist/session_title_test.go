package promptassist

import "testing"

func TestCleanAndFallbackSummarizeTitle(t *testing.T) {
	rawZh := "<think>思考中...</think>\n标题：“修复网关 CORS 与单元测试”."
	if got := cleanSummarizedTitle(rawZh, "zh-CN"); got != "修复网关 CORS 与单元测试" {
		t.Errorf("cleanSummarizedTitle zh-CN = %q, want %q", got, "修复网关 CORS 与单元测试")
	}

	rawEn := `Title: "Optimize SSE Streaming & Worktree Isolation".`
	if got := cleanSummarizedTitle(rawEn, "en-US"); got != "Optimize SSE Streaming & Worktree Isolation" {
		t.Errorf("cleanSummarizedTitle en-US = %q, want %q", got, "Optimize SSE Streaming & Worktree Isolation")
	}

	if got := cleanSummarizedTitle("   <think>only thought</think>  ", "en-US"); got != "" {
		t.Errorf("expected empty cleaned title, got %q", got)
	}

	fbZh := fallbackSummarizeTitle("请帮我排查 desktop_gateway.go 里的 CORS 配置问题。顺便加上单元测试。", "zh-CN")
	if fbZh == "" || fbZh == "新任务会话" {
		t.Fatalf("expected meaningful zh fallback title, got %q", fbZh)
	}

	fbEn := fallbackSummarizeTitle("```go\nfmt.Println()\n```\nFix race condition in worktree manager. Also update docs.", "en-US")
	if fbEn != "Fix race condition in worktree manager" {
		t.Errorf("fallbackSummarizeTitle en-US = %q, want %q", fbEn, "Fix race condition in worktree manager")
	}

	if got := fallbackSummarizeTitle("<think>thinking</think>", "zh-CN"); got != "新任务会话" {
		t.Errorf("expected default zh fallback, got %q", got)
	}
	if got := fallbackSummarizeTitle("   ", "en-US"); got != "New Task Session" {
		t.Errorf("expected default en fallback, got %q", got)
	}
}

func TestCleanSummarizedTitleRejectsRawJSON(t *testing.T) {
	raw := `{"choices":[{"finish_reason":"length","index":0,"logprobs":null,"message":{"content":"","reasoning_content":"..."}}]}`
	if title := cleanSummarizedTitle(raw, "zh-CN"); title != "" {
		t.Fatalf("expected cleanSummarizedTitle to reject raw JSON, got %q", title)
	}
}
