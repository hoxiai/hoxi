package worktree

import "testing"

func TestCleanGeneratedCommitMessage(t *testing.T) {
	t.Parallel()
	truncatedReasoningJSON := `{"choices":[{"finish_reason":"length","index":0,"logprobs":null,"message":{"content":"","reasoning_content":"The user wants a git commit message..."}}]}`
	if msg := cleanGeneratedCommitMessage(truncatedReasoningJSON); msg != "" {
		t.Fatalf("expected cleanGeneratedCommitMessage to reject raw JSON, got %q", msg)
	}
	if got := cleanGeneratedCommitMessage("feat(git): 新增底部一键提交并推送功能"); got != "feat(git): 新增底部一键提交并推送功能" {
		t.Fatalf("unexpected cleaned commit message: %q", got)
	}
	if got := cleanGeneratedCommitMessage("<think>plan</think>\nCommit message: fix(runner): guard nil session."); got != "fix(runner): guard nil session" {
		t.Fatalf("unexpected cleaned commit message: %q", got)
	}
}
