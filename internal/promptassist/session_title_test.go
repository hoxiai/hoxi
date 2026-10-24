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
	if got := cleanSummarizedTitle("<thinking>**Drafting title****Refining concise wording**</thinking>\n修复 Codex Thinking 标签兼容", "zh-CN"); got != "修复 Codex Thinking 标签兼容" {
		t.Errorf("cleanSummarizedTitle with <thinking> = %q, want %q", got, "修复 Codex Thinking 标签兼容")
	}
	if got := cleanSummarizedTitle("<thinking>**Running build and mobile checks**", "en-US"); got != "" {
		t.Errorf("expected empty cleaned title for unclosed <thinking>, got %q", got)
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

func TestHandleSummarizeTitleSkipsLLMForDirectShortTitles(t *testing.T) {
	client := &fakeLLM{text: "Should Not Be Called"}
	svc := NewService(client)

	bodyZh := post(t, svc.HandleSummarizeTitle, summarizeTitleRequest{
		ProviderID: "p", ModelID: "m", Prompt: "修复登录按钮对齐", Locale: "zh-CN",
	})
	if client.calls != 0 || bodyZh != `{"ok":true,"title":"修复登录按钮对齐"}`+"\n" {
		t.Fatalf("expected direct short zh title with 0 LLM calls (got %d calls): %s", client.calls, bodyZh)
	}

	bodyEn := post(t, svc.HandleSummarizeTitle, summarizeTitleRequest{
		ProviderID: "p", ModelID: "m", Prompt: "Fix login button alignment", Locale: "en-US",
	})
	if client.calls != 0 || bodyEn != `{"ok":true,"title":"Fix login button alignment"}`+"\n" {
		t.Fatalf("expected direct short en title with 0 LLM calls (got %d calls): %s", client.calls, bodyEn)
	}
}

