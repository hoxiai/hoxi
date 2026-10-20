package provider

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/worktree"
)

func TestBuildObserverAuditPromptFencesGuidanceAndNotesOmittedFiles(t *testing.T) {
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
	prompt := buildObserverAuditPrompt(auditCodeRequest{WorkerProviderID: "p", WorkerModelID: "m", Locale: "zh-CN"})
	if strings.Contains(prompt, "review_guidance") {
		t.Fatal("no guidance block expected without REVIEW.md")
	}
	if !strings.Contains(prompt, "请使用简明扼要的中文") {
		t.Fatal("locale hint lost in refactor")
	}
}

func TestAuditDiffsFromReviewCopiesEveryField(t *testing.T) {
	out := auditDiffsFromReview([]worktree.ReviewFileDiff{
		{File: "a.go", Before: "old", After: "new", Additions: 2, Deletions: 1},
	})
	if len(out) != 1 || out[0] != (auditDiffFile{File: "a.go", Before: "old", After: "new", Additions: 2, Deletions: 1}) {
		t.Fatalf("unexpected conversion: %+v", out)
	}
}
