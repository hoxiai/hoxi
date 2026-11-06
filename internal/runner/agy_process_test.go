package runner

import "testing"

func TestInferOfficialRunnerModelSpecClaudeContextLimits(t *testing.T) {
	t.Parallel()
	cases := []struct {
		slug    string
		ctx     int
		out     int
		inCost  float64
		outCost float64
	}{
		// Antigravity slugs carry an effort suffix; the 5.x window is 1M.
		{"claude-opus-5-5-high", 1000000, 128000, 5.00, 25.00},
		{"claude-sonnet-5-5-medium", 1000000, 128000, 3.00, 15.00},
		// Claude Code ids, including its explicit long-context tag.
		{"claude-opus-5-5", 1000000, 128000, 5.00, 25.00},
		{"claude-fable-5-1[1m]", 1000000, 128000, 5.00, 25.00},
		{"claude-sonnet-4-5[1m]", 1000000, 64000, 3.00, 15.00},
		// Older or unversioned ids keep the 200K window.
		{"claude-opus-4-7", 200000, 64000, 5.00, 25.00},
		{"claude-3-5-sonnet", 200000, 64000, 3.00, 15.00},
		{"claude-haiku-5-5", 200000, 64000, 0.80, 4.00},
		{"sonnet", 200000, 64000, 3.00, 15.00},
		{"haiku", 200000, 64000, 0.80, 4.00},
	}
	for _, tc := range cases {
		ctx, out, inCost, outCost, _, _ := inferOfficialRunnerModelSpec(tc.slug)
		if ctx != tc.ctx || out != tc.out || inCost != tc.inCost || outCost != tc.outCost {
			t.Errorf("%s: got ctx=%d out=%d cost=%.2f/%.2f, want ctx=%d out=%d cost=%.2f/%.2f",
				tc.slug, ctx, out, inCost, outCost, tc.ctx, tc.out, tc.inCost, tc.outCost)
		}
	}
}

func TestBuildAgyModelItemsReportsClaudeFiveWindow(t *testing.T) {
	t.Parallel()
	items := buildAgyModelItems([]string{"claude-opus-5-5-high", "gemini-3-pro"}, nil)
	byID := map[string]int{}
	for _, it := range items {
		byID[it.ID] = it.ContextLimit
	}
	if byID["claude-opus-5-5-high"] != 1000000 {
		t.Fatalf("claude-opus-5-5-high context = %d, want 1000000", byID["claude-opus-5-5-high"])
	}
	if byID["gemini-3-pro"] != 1000000 {
		t.Fatalf("gemini-3-pro context = %d, want 1000000", byID["gemini-3-pro"])
	}
}

func TestClaudeMajorVersion(t *testing.T) {
	t.Parallel()
	cases := map[string]int{
		"claude-opus-5-5-high": 5,
		"claude-3-5-sonnet":    3,
		"claude-fable-5-1[1m]": 5,
		"claude-opus-10-1":     10,
		"opus":                 0,
		"claude":               0,
		"gemini-3-pro":         0,
	}
	for slug, want := range cases {
		if got := claudeMajorVersion(slug); got != want {
			t.Errorf("claudeMajorVersion(%q) = %d, want %d", slug, got, want)
		}
	}
}
