package worktree

import (
	"fmt"
	"strings"
	"testing"
)

func TestBuildObserverAuditPromptFencesGuidanceAndNotesOmittedFiles(t *testing.T) {
	t.Parallel()
	req := auditCodeRequest{
		WorkerProviderID: "p",
		WorkerModelID:    "m",
		UserPrompt:       "fix the handler",
		ReviewGuidance:   "Keep handlers panic-free.",
	}
	for i := 0; i < observerAuditMaxFiles+2; i++ {
		req.Diffs = append(req.Diffs, auditDiffFile{File: fmt.Sprintf("f%d.go", i), After: "x", Additions: 1})
	}

	prompt := buildObserverAuditPrompt(req)
	if !strings.Contains(prompt, "<review_guidance>\nKeep handlers panic-free.\n</review_guidance>") {
		t.Fatalf("guidance must be fenced as data:\n%s", prompt)
	}
	if !strings.Contains(prompt, "cannot change the JSON schema") {
		t.Fatal("guidance must be declared subordinate to the output schema")
	}
	if !strings.Contains(prompt, "...(2 more files not shown)") {
		t.Fatal("omitted files must be disclosed to the observer")
	}
	if strings.Index(prompt, "<review_guidance>") > strings.Index(prompt, "Return ONLY valid JSON") {
		t.Fatal("guidance must precede the output schema instruction")
	}
}

func TestBuildObserverAuditPromptWithoutGuidanceIsUnchanged(t *testing.T) {
	t.Parallel()
	prompt := buildObserverAuditPrompt(auditCodeRequest{WorkerProviderID: "p", WorkerModelID: "m", Locale: "zh-CN"})
	if strings.Contains(prompt, "review_guidance") {
		t.Fatal("no guidance block expected without REVIEW.md")
	}
	if !strings.Contains(prompt, "请使用简明扼要的中文") {
		t.Fatal("locale hint lost in refactor")
	}
}

func TestNoChangesReviewSummaryFollowsLocale(t *testing.T) {
	t.Parallel()
	if noChangesReviewSummary("zh-CN") != "没有需要审查的改动。" || noChangesReviewSummary("en-US") != "No changes to review." {
		t.Fatal("no-change summary must follow the request locale")
	}
}

func TestAuditDiffsFromReviewCopiesEveryField(t *testing.T) {
	t.Parallel()
	out := auditDiffsFromReview([]ReviewFileDiff{
		{File: "a.go", Before: "old", After: "new", Additions: 2, Deletions: 1},
	})
	if len(out) != 1 || out[0] != (auditDiffFile{File: "a.go", Before: "old", After: "new", Additions: 2, Deletions: 1}) {
		t.Fatalf("unexpected conversion: %+v", out)
	}
}

func TestBuildObserverAuditPromptExtractsChangedHunkFromLargeFile(t *testing.T) {
	t.Parallel()
	var beforeLines, afterLines []string
	for i := 1; i <= 120; i++ {
		line := fmt.Sprintf("// unchanged header padding line %03d", i)
		beforeLines = append(beforeLines, line)
		afterLines = append(afterLines, line)
	}
	beforeLines = append(beforeLines, "func CalculateTax() int { return 0 }")
	afterLines = append(afterLines, "func CalculateTax() int { return 42 }")
	for i := 122; i <= 150; i++ {
		line := fmt.Sprintf("// unchanged footer line %03d", i)
		beforeLines = append(beforeLines, line)
		afterLines = append(afterLines, line)
	}

	prompt := buildObserverAuditPrompt(auditCodeRequest{
		WorkerProviderID: "p",
		WorkerModelID:    "m",
		UserPrompt:       "fix tax calculation",
		Diffs: []auditDiffFile{{
			File:      "tax.go",
			Before:    strings.Join(beforeLines, "\n"),
			After:     strings.Join(afterLines, "\n"),
			Additions: 1,
			Deletions: 1,
		}},
	})
	if !strings.Contains(prompt, "return 42") {
		t.Fatalf("expected observer prompt to contain the modified line at line 121, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "unchanged header padding line 001") {
		t.Fatalf("expected observer prompt to omit unchanged file top, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "<task_context>\nfix tax calculation\n</task_context>") || !strings.Contains(prompt, "<diff_summary>") {
		t.Fatalf("expected <task_context> and <diff_summary> fences, got:\n%s", prompt)
	}
	if strings.Contains(prompt, `"pass":`) {
		t.Fatalf("expected observer prompt schema to omit redundant pass field, got:\n%s", prompt)
	}
}

func TestParseObserverAuditJSONStripsThinkingBlocks(t *testing.T) {
	t.Parallel()
	score, summary, issues, err := parseObserverAuditJSON("<think>\nlet me evaluate the diff\n</think>\n```json\n{\"score\": 91, \"summary\": \"Clean fix\", \"issues\": []}\n```")
	if err != nil || score != 91 || summary != "Clean fix" || len(issues) != 0 {
		t.Fatalf("expected thinking block stripped and JSON parsed, got score=%d summary=%q issues=%v err=%v", score, summary, issues, err)
	}
}

