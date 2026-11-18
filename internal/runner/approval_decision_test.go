package runner

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/testutil/execstub"
	"github.com/hoxiai/hoxi/internal/worktree"
)

func req(perm string, patterns ...string) map[string]interface{} {
	list := make([]interface{}, len(patterns))
	for i, p := range patterns {
		list[i] = p
	}
	return map[string]interface{}{"permission": perm, "patterns": list}
}

func toolReq(tool string, meta map[string]interface{}) map[string]interface{} {
	if meta == nil {
		meta = map[string]interface{}{}
	}
	meta["tool"] = tool
	return map[string]interface{}{"permission": "tool", "patterns": []interface{}{tool}, "metadata": meta}
}

func TestQoderAndCodeBuddyRunInBuildMode(t *testing.T) {
	binDir := t.TempDir()
	execstub.Write(t, binDir, "qodercli", "#!/bin/sh\ncat >/dev/null\nprintf '{\"type\":\"result\",\"is_error\":false,\"result\":\"ok\"}\\n'\nexit 0\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	repo := initTestGitRepo(t)
	storeDir := filepath.Join(t.TempDir(), "sessions")
	svc := NewService(repo, worktree.NewManager(repo), nil, storeDir)
	defer svc.CloseAll()

	rec := SessionRecord{
		ID:           "ses_qoder_build",
		Runtime:      string(RuntimeQoder),
		ProjectDir:   repo,
		WorktreePath: repo,
		SandboxMode:  "local",
		ModelID:      "auto",
		Mode:         "build",
	}
	if err := svc.Store().Put(rec); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	httpReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/ses_qoder_build/prompt", strings.NewReader(`{"text":"edit main.go"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httpReq)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for Qoder turn in build mode, got %d: %s", w.Code, w.Body.String())
	}
}

func TestOpaqueInterpreterVariantsAsk(t *testing.T) {
	for _, mode := range []string{ApprovalModeAuto, ApprovalModeTrusted} {
		if !approvalAutoApproves(req("bash", `python3.12 -c 'print(1)'`), ApprovalPolicy{Mode: mode}, nil, "") {
			t.Errorf("%s mode should run a read-only version-suffixed inline interpreter", mode)
		}
	}
	for _, mode := range []string{ApprovalModeAuto, ApprovalModeTrusted} {
		commands := []string{`awk 'BEGIN {system("id")}'`, `awk 'BEGIN{print "id" | "sh"}'`}
		if mode == ApprovalModeAuto {
			commands = append(commands, `python3.12 -c 'import os; os.mkdir(0)'`, `node20 -e 'require("fs").rmSync("x")'`)
		}
		for _, command := range commands {
			t.Run(mode+"/"+command, func(t *testing.T) {
				item := map[string]interface{}{"permission": "bash", "patterns": []string{command}}
				if approvalAutoApproves(item, ApprovalPolicy{Mode: mode}, nil, "") {
					t.Fatalf("automatically approved inline interpreter command in %s", mode)
				}
			})
		}
	}
}

func TestCrossDirectoryCDAndSkillsApproval(t *testing.T) {
	worktreeDir := t.TempDir()
	siblingRepo := t.TempDir()
	deployCmd := "cd " + siblingRepo + " && NODE_OPTIONS=--max-old-space-size=32768 /Users/u/.agents/skills/release-skill/scripts/deploy-site.sh 1.1.24 2>&1 | tail -70"
	for _, mode := range []string{ApprovalModeAuto, ApprovalModeTrusted} {
		if !approvalAutoApproves(req("bash", deployCmd), ApprovalPolicy{Mode: mode}, nil, worktreeDir) {
			t.Errorf("cross-directory deploy script asked in %s mode", mode)
		}
		if approvalAutoApproves(req("bash", "cd /etc && cat passwd"), ApprovalPolicy{Mode: mode}, nil, worktreeDir) {
			t.Errorf("cd into /etc was auto-approved in %s mode", mode)
		}
	}

	svc := NewService(worktreeDir, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	if err := svc.store.Put(SessionRecord{ID: "ses_skill_test", WorktreePath: worktreeDir}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		tool        string
		path        string
		destructive bool
	}{
		{"read_file", "/Users/u/.agents/skills/release-skill/SKILL.md", false},
		{"read_file", filepath.Join(siblingRepo, ".git", "logs", "HEAD"), false},
		{"read_file", "/etc/passwd", true},
		{"edit_file", "/Users/u/.agents/hooks.json", true},
		{"edit_file", "/Users/u/.agents/skills/release-skill/scripts/deploy.sh", true},
		{"write_file", filepath.Join(worktreeDir, ".hoxi", "skills", "foo", "run.sh"), true},
		{"write_file", filepath.Join(worktreeDir, ".hoxi", "attachments", "paste.png"), false},
		{"edit_file", filepath.Join(worktreeDir, ".git", "config"), true},
	} {
		_, _, gotDestructive, _ := svc.inspectHookToolRisk("ses_skill_test", tc.tool, map[string]interface{}{"Path": tc.path})
		if gotDestructive != tc.destructive {
			t.Errorf("inspectHookToolRisk(%s, %s) destructive = %v, want %v", tc.tool, tc.path, gotDestructive, tc.destructive)
		}
	}
}

func TestDataArgumentsInSubshellsAndPrefixesAreNotDestructive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, cmd := range []string{
		`(cd pkg && git commit -m "fix: avoid rm -rf false positive")`,
		`LC_ALL=C rg "rm -rf|sudo|\.env" src`,
		`env LC_ALL=C rg "rm -rf$" src`,
		`timeout 5 rg "^(sudo|rm -rf)$" src`,
		`time git commit -m "fix: rm -rf false positive$"`,
		`diff <(rg "rm -rf" a.txt) <(rg "sudo" b.txt)`,
		`bash -c 'rg "rm -rf|sudo|\.env" src'`,
	} {
		if isDestructiveCommand(cmd) {
			t.Fatalf("command with data argument inside subshell/prefix was treated as destructive: %s", cmd)
		}
	}
	for _, cmd := range []string{
		`(cd pkg && rg "rm -rf|sudo|\.env" internal/)`,
		`env LC_ALL=C rg "rm -rf$" src`,
	} {
		if !approvalAutoApproves(req("bash", cmd), ApprovalPolicy{Mode: ApprovalModeAuto}, nil, dir) {
			t.Fatalf("safe subshell/prefix data-argument command was not auto-approved: %s", cmd)
		}
	}
	for _, cmd := range []string{
		`bash -c 'rm -rf ./dist'`,
		`(cd web && rm -rf dist)`,
	} {
		if !isDestructiveCommand(cmd) {
			t.Fatalf("destructive command inside subshell/bash -c was not detected: %s", cmd)
		}
	}
	if approvalAutoApproves(req("bash", `(cd pkg && cat .env)`), ApprovalPolicy{Mode: ApprovalModeAuto}, nil, dir) {
		t.Fatal("real .env file read inside subshell must still ask")
	}
}

func TestHoxiDataAndNativeImageApprovalAlignment(t *testing.T) {
	t.Parallel()
	wt := t.TempDir()
	svc := NewService(wt, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	rec := SessionRecord{ID: "ses_img_1", WorktreePath: wt}

	getDataReq := toolReq("hoxi_data", map[string]interface{}{"args": `{"action":"http_request","profile":"shopify","method":"GET","endpoint":"/orders"}`})
	postDataReq := toolReq("hoxi_data", map[string]interface{}{"args": `{"action":"http_request","profile":"shopify","method":"POST","endpoint":"/orders"}`})
	if !approvalAutoApproves(getDataReq, ApprovalPolicy{Mode: ApprovalModeAuto}, nil, wt) {
		t.Fatal("expected hoxi_data GET to auto-approve in auto mode")
	}
	if approvalAutoApproves(getDataReq, ApprovalPolicy{Mode: ApprovalModeSmart}, nil, wt) {
		t.Fatal("expected hoxi_data GET to ask in smart mode")
	}
	if approvalAutoApproves(postDataReq, ApprovalPolicy{Mode: ApprovalModeAuto}, nil, wt) {
		t.Fatal("hoxi_data POST must ask even in auto mode")
	}

	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeSmart})
	if !svc.nativeImageCallAutoAllowed(rec, map[string]interface{}{"prompt": "app icon"}) {
		t.Fatal("expected default .hoxi/attachments/ hoxi_image to run without nagging in smart mode")
	}
	if svc.nativeImageCallAutoAllowed(rec, map[string]interface{}{"prompt": "edit", "inputPaths": []interface{}{".env"}}) {
		t.Fatal("hoxi_image must never auto-allow secret inputPaths")
	}
	if svc.nativeImageCallAutoAllowed(rec, map[string]interface{}{"prompt": "hook", "outputPath": ".git/hooks/pre-commit"}) {
		t.Fatal("hoxi_image must never auto-allow protected outputPath")
	}
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeManual})
	if svc.nativeImageCallAutoAllowed(rec, map[string]interface{}{"prompt": "app icon"}) {
		t.Fatal("manual mode must still prompt for hoxi_image")
	}

	if got := summarizeHookToolAction("hoxi_browser", map[string]interface{}{"action": "click", "selector": "@e3"}); got != "click @e3" {
		t.Fatalf("unexpected hoxi_browser approval summary: %q", got)
	}
	if got := summarizeHookToolAction("hoxi_data", map[string]interface{}{"action": "http_request", "method": "delete", "profile": "shopify", "endpoint": "/orders/1"}); got != "DELETE shopify /orders/1" {
		t.Fatalf("unexpected hoxi_data approval summary: %q", got)
	}
}
