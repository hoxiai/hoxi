package runner

import (
	"reflect"
	"strings"
	"testing"
)

func TestNumericLoopValuesPreserveIntegerSequence(t *testing.T) {
	for expr, want := range map[string][]string{
		"{1..3}": {"1", "2", "3"}, "{3..1}": {"3", "2", "1"},
		"{-2..1}": {"-2", "-1", "0", "1"}, "{1..5..2}": {"1", "3", "5"},
		"{5..1..2}": {"5", "3", "1"}, "$(seq 3)": {"1", "2", "3"},
		"$(seq 0 2)": {"0", "1", "2"}, "$(seq 1 2 5)": {"1", "3", "5"},
		"$(seq 3 -1 1)": {"3", "2", "1"}, "{7..7}": {"7"},
	} {
		got, ok := numericLoopValues(expr)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v (%v), want %v", expr, got, ok, want)
		}
	}
	for _, expr := range []string{"{001..003}", "{1..3..0}", "{1..3..-1}", "$(seq 1 0 3)", "$(seq 3 1)", "$(seq 0.1 0.3)", "$(seq -w 1 3)", "{1..9999999999}", "$(seq 1 3; cat .env)"} {
		if got, ok := numericLoopValues(expr); ok {
			t.Errorf("unsupported sequence %q was accepted as %v", expr, got)
		}
	}
	for _, expr := range []string{"{1..65}", "$(seq 1 65)", "{1..999999999}"} {
		if got, ok := numericLoopValues(expr); !ok || !reflect.DeepEqual(got, []string{seqLoopPlaceholder}) {
			t.Errorf("oversized sequence %q was truncated/expanded: %v", expr, got)
		}
	}
}

func TestBoundedNumericLoopsRunWithoutApproval(t *testing.T) {
	for _, command := range []string{
		`for i in {1..3}; do node -e "console.log($i)"; done`,
		`for i in {3..1}; do node -e "console.log('$i')"; done`,
		`for i in {-2..1}; do node -e "console.log($i)"; done`,
		`for i in {1..5..2}; do node -e "console.log($i)"; done`,
		`for i in $(seq 3); do node -e "console.log($i)"; done`,
		`for i in $(seq 1 3); do node -e "console.log($i)"; done`,
		`for i in $(seq 3 -1 1); do python3 -c "print($i)"; done`,
		`for i in {1..64}; do node -e "console.log($i)"; done`,
		`for i in {1..3}; do for j in $(seq 2); do node -e "console.log($i,$j)"; done; done`,
	} {
		if !approvalAutoApproves(bashRequest(command), ApprovalPolicy{Mode: ApprovalModeAuto}, nil, t.TempDir()) {
			t.Errorf("bounded read-only loop asked: %s", command)
		}
	}
}

func TestNumericLoopsKeepRuntimeCodeAndDestructionGated(t *testing.T) {
	for _, command := range []string{
		`for i in {1..65}; do node -e "console.log($i)"; done`,
		`for i in $(seq 1 65); do node -e "console.log($i)"; done`,
		`for i in $(seq 0.1 0.3); do node -e "console.log($i)"; done`,
		`for i in $(cat values.txt); do node -e "console.log($i)"; done`,
		`for i in {1..3}; do i=$(cat code.txt); node -e "console.log('$i')"; done`,
		`for i in {1..3}; do for i in unknown; do node -e "console.log('$i')"; done; done`,
		`for i in {1..3}; do node -e "require('fs').writeFileSync('out'+$i,'x')"; done`,
		`for i in {1..3}; do node -e "require('child_process').execSync('ls')"; done`,
		`for i in {1..3}; do node -e "console.log(require('fs').readFileSync('.env','utf8'))"; done`,
		`for i in {1..3}; do rm -rf "file_$i"; done`,
		`for i in $(seq 1 40); do rm "file_$i"; done`,
		`for i in {1..3}; do $i --version; done`,
	} {
		if approvalAutoApproves(bashRequest(command), ApprovalPolicy{Mode: ApprovalModeAuto}, nil, t.TempDir()) {
			t.Errorf("unverified numeric loop ran directly: %s", command)
		}
	}
}

func TestNumericLoopJudgmentSkipsQuotedDataAndBoundsGrowth(t *testing.T) {
	for _, command := range []string{
		`node -e 'console.log("for i in {1..3}; do echo $i; done")'`,
		`node -e 'console.log("for i in $(seq 1 3); do echo $i; done")'`,
		`for i in '{1..3}'; do echo "$i"; done`,
		"# for i in {1..3}; do echo $i; done\nnode --version",
		"node <<'EOF'\nfor i in {1..3}; do echo $i; done\nEOF",
		`for i in {1..3}; echo missing-do; done`,
		`for i in {1..3}; do echo missing-done`,
	} {
		if got := normalizeNumericForHeaders(command); got != command {
			t.Errorf("data/malformed loop was rewritten: %s", command)
		}
	}
	command := `for i in {1..64}; do for j in {1..64}; do for k in {1..64}; do node -e "console.log($i,$j,$k)"; done; done; done`
	expanded, _ := concretizeLiterals(command)
	if len(expanded) > maxLiteralLoopBytes || !strings.Contains(expanded, "for ") {
		t.Fatalf("nested numeric loop lost its judgment bound: %d bytes", len(expanded))
	}
}

func TestClaudeHookAllowsBoundedLoopsAndQuotedDirectories(t *testing.T) {
	f := newClaudeGateFixture(t)
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeAuto})
	for _, command := range []string{
		`for i in {1..3}; do node -e "console.log($i)"; done`,
		`for i in $(seq 1 3); do node -e "console.log($i)"; done`,
		`cd "admin/my locale" && node -e "console.log(1)"`,
	} {
		if got := f.decide(t, f.payload("Bash", command, "", "cc-uuid-1")); got != "allow|" {
			t.Errorf("read-only hook command asked: %q (%s)", got, command)
		}
	}
}
