package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnattendedSharedCases(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".opencode", "lib", "unattended.cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Command  string `json:"command"`
		Worktree bool   `json:"worktree"`
		Expect   string `json:"expect"`
		Category string `json:"category"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(t.TempDir(), ".hoxi", "worktrees", "ses")
	for _, c := range cases {
		wt := ""
		if c.Worktree {
			wt = worktree
		}
		item := map[string]interface{}{
			"permission": "bash",
			"patterns":   []string{c.Command},
			"metadata":   map[string]interface{}{"command": c.Command},
		}
		allow, category := unattendedDecision(item, wt)
		got := "deny"
		if allow {
			got = "allow"
		}
		if got != c.Expect || (c.Expect == "deny" && category != c.Category) {
			t.Errorf("%q: got %s/%s, want %s/%s", c.Command, got, category, c.Expect, c.Category)
		}
	}
}

func TestUnattendedDeniesOutsideEditAndUnknownTool(t *testing.T) {
	worktree := filepath.Join(t.TempDir(), ".hoxi", "worktrees", "ses")
	edit := map[string]interface{}{"permission": "edit", "patterns": []string{"/etc/hosts"}}
	if allow, category := unattendedDecision(edit, worktree); allow || category != unattendedCategoryEdit {
		t.Fatalf("outside edit: allow=%v category=%s", allow, category)
	}
	inside := map[string]interface{}{"permission": "edit", "patterns": []string{filepath.Join(worktree, "main.go")}}
	if allow, _ := unattendedDecision(inside, worktree); !allow {
		t.Fatal("in-worktree edit was denied")
	}
	mcp := map[string]interface{}{"permission": "tool", "patterns": []string{"some_mcp_tool"}}
	if allow, category := unattendedDecision(mcp, worktree); allow || category != unattendedCategoryTool {
		t.Fatalf("mcp tool: allow=%v category=%s", allow, category)
	}
	aws := map[string]interface{}{"permission": "read", "patterns": []string{"/Users/u/.aws/credentials"}}
	if allow, category := unattendedDecision(aws, worktree); allow || category != unattendedCategorySecret {
		t.Fatalf("aws read: allow=%v category=%s", allow, category)
	}
	extRoutine := map[string]interface{}{"permission": "external_directory", "patterns": []string{"/tmp/hoxi_go_test.log"}}
	if allow, category := unattendedDecision(extRoutine, worktree); !allow || category != "" {
		t.Fatalf("routine external_directory: allow=%v category=%s", allow, category)
	}
	extSecret := map[string]interface{}{"permission": "external_directory", "patterns": []string{"/Users/u/.ssh/*"}}
	if allow, category := unattendedDecision(extSecret, worktree); allow || category != unattendedCategorySecret {
		t.Fatalf("secret external_directory: allow=%v category=%s", allow, category)
	}
	extSensitive := map[string]interface{}{"permission": "external_directory", "patterns": []string{"/etc/hosts"}}
	if allow, category := unattendedDecision(extSensitive, worktree); allow || category != unattendedCategoryTool {
		t.Fatalf("sensitive external_directory: allow=%v category=%s", allow, category)
	}
	todo := map[string]interface{}{"permission": "todowrite", "patterns": []string{"*"}}
	if allow, category := unattendedDecision(todo, worktree); !allow || category != "" {
		t.Fatalf("todowrite: allow=%v category=%s", allow, category)
	}
}

func TestUnattendedModeRequiresIsolation(t *testing.T) {
	pol := ApprovalPolicy{Mode: ApprovalModeUnattended}
	item := map[string]interface{}{"permission": "bash", "patterns": []string{"bash -c 'echo ok'"}}
	if approvalAutoApprovesIsolated(item, pol, nil, t.TempDir(), false) {
		t.Fatal("unattended allowed a command for a session that is not isolated")
	}
	worktree := filepath.Join(t.TempDir(), ".hoxi", "worktrees", "ses")
	if !approvalAutoApprovesIsolated(item, pol, nil, worktree, true) {
		t.Fatal("unattended denied an ordinary command inside an isolated sandbox")
	}
}

func TestUnattendedDynamicJudgesWholeOpenCodeCommand(t *testing.T) {
	worktree := filepath.Join(t.TempDir(), ".hoxi", "worktrees", "ses")
	whole := "git commit -m \"$(cat <<'EOF'\nIt's done\nEOF\n)\""
	// OpenCode splits the command into pieces that do not parse alone.
	item := map[string]interface{}{
		"permission": "bash",
		"patterns":   []string{"git commit -m \"$(cat <<'EOF'", "It's done", "EOF", ")\""},
		"metadata":   map[string]interface{}{"command": whole},
	}
	if allow, category := unattendedDecision(item, worktree); !allow {
		t.Fatalf("a heredoc commit message was refused: %s", category)
	}
	item["metadata"] = map[string]interface{}{"command": "G=git; $G push"}
	item["patterns"] = []string{"G=git", "$G push"}
	if allow, category := unattendedDecision(item, worktree); allow || category != unattendedCategoryDynamic {
		t.Fatalf("variable command name: allow=%v category=%s", allow, category)
	}
}

func TestUnattendedUnparsableCommandIsDynamic(t *testing.T) {
	for _, cmd := range []string{"echo 'unterminated", "cat <<EOF\nno end", "echo $(git push"} {
		if got := unattendedDynamicCategory(cmd); got != unattendedCategoryDynamic {
			t.Errorf("%q: got %q, want dynamic", cmd, got)
		}
	}
}

func TestUnattendedDynamicReasonDoesNotEchoCommand(t *testing.T) {
	reason := unattendedDenyReason(unattendedCategoryDynamic)
	if strings.Contains(reason, "GITHUB_TOKEN") || !strings.Contains(reason, "dynamic code execution") {
		t.Fatalf("reason: %s", reason)
	}
}
