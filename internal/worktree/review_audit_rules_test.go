package worktree

import (
	"strings"
	"testing"
)

func TestEvaluateStaticCodeRulesAndParseObserverAudit(t *testing.T) {
	t.Parallel()
	bigFileLines := strings.Repeat("const x = 1;\n", 805)
	diffs := []auditDiffFile{
		{
			File:      "web/src/App.tsx",
			Before:    "const a = 1;\n",
			After:     "const a = 2;\n// TODO: implement rest of logic\n",
			Additions: 2,
			Deletions: 1,
		},
		{
			File:      "internal/foo/big.go",
			Before:    "package foo\n",
			After:     bigFileLines,
			Additions: 805,
		},
		{
			File:      "web/src/components/icons/svg.tsx",
			After:     bigFileLines,
			Additions: 805,
		},
	}

	deductionZh, issuesZh := evaluateStaticCodeRules(diffs, 2, "TS2322 type mismatch", "zh-CN")
	if deductionZh < 60 || len(issuesZh) != 3 {
		t.Fatalf("expected deduction >= 60 and 3 issues (LSP + lazy + >800 lines, excluding icons), got deduction=%d issues=%v", deductionZh, issuesZh)
	}

	deductionEn, issuesEn := evaluateStaticCodeRules(diffs, 3, "undefined symbol", "en-US")
	if deductionEn < 60 || len(issuesEn) != 3 {
		t.Fatalf("expected English static rules issues, got deduction=%d issues=%v", deductionEn, issuesEn)
	}

	score, summary, parsedIssues, err := parseObserverAuditJSON("```json\n{\"score\": 68, \"pass\": false, \"summary\": \"Missing error handling\", \"issues\": [\"Unchecked null pointer\"]}\n```")
	if err != nil || score != 68 || summary != "Missing error handling" || len(parsedIssues) != 1 {
		t.Fatalf("unexpected parsed observer JSON: score=%d summary=%q issues=%v", score, summary, parsedIssues)
	}

	if s, _, _, err := parseObserverAuditJSON("92"); err != nil || s != 92 {
		t.Errorf("expected numeric score 92, got %d", s)
	}
}

func TestParseObserverAuditScoreValidation(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		"``` json\n{\"score\":68,\"summary\":\"ok\"}\n```",
		"```JSON\n{\"score\":68,\"summary\":\"ok\"}\n```",
		"```json {\"score\":68,\"summary\":\"ok\"}```",
	} {
		t.Run(text, func(t *testing.T) {
			score, summary, _, err := parseObserverAuditJSON(text)
			if err != nil || score != 68 || summary != "ok" {
				t.Fatalf("expected fenced JSON with flexible language tag, got %d, %q, %v", score, summary, err)
			}
		})
	}
	for _, text := range []string{`{"score":0}`, `{"score":100}`, "0", "100"} {
		t.Run(text, func(t *testing.T) {
			score, _, _, err := parseObserverAuditJSON(text)
			if err != nil || (score != 0 && score != 100) {
				t.Fatalf("expected valid boundary score, got %d, %v", score, err)
			}
		})
	}
	for _, text := range []string{
		"", "Looks good overall", "{}", "null", `{"score":null}`, `{"score":"0"}`,
		`{"score":-1}`, `{"score":101}`, `{"score":1.5}`, `{"score":NaN}`,
		`{"score":Infinity}`, `{"score":1e999}`, `{"score":0`, `{"score":0} garbage`,
		`{"score":0,"issues":"invalid"}`, "NaN", "Inf", "-1", "101",
	} {
		t.Run(text, func(t *testing.T) {
			if _, _, _, err := parseObserverAuditJSON(text); err == nil {
				t.Fatalf("expected invalid audit response to fail: %q", text)
			}
		})
	}
}
