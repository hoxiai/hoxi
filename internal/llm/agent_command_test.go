package llm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAndExpandSlashCommands(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	wsDir := t.TempDir()

	// 1. Nested Claude Markdown command (.claude/commands/fe/audit.md -> fe:audit)
	claudeCmdDir := filepath.Join(wsDir, ".claude", "commands", "fe")
	if err := os.MkdirAll(claudeCmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	feAudit := "---\ndescription: Audit frontend accessibility\nagent: plan\nmodel: anthropic/claude-sonnet-4-5\nargument-hint: <component> <wcag-level>\n---\nAudit component $1 against WCAG $2.\nFull raw args: $ARGUMENTS"
	if err := os.WriteFile(filepath.Join(claudeCmdDir, "audit.md"), []byte(feAudit), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. Hoxi Markdown command without placeholders (.hoxi/commands/changelog.md)
	hoxiCmdDir := filepath.Join(wsDir, ".hoxi", "commands")
	if err := os.MkdirAll(hoxiCmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hoxiCmdDir, "changelog.md"), []byte("# Generate Changelog\nSummarize recent commits cleanly."), 0o644); err != nil {
		t.Fatal(err)
	}

	// 3. OpenCode CJK Markdown command (.opencode/commands/估值.md -> /估值)
	ocCmdDir := filepath.Join(wsDir, ".opencode", "commands")
	if err := os.MkdirAll(ocCmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	valuationCmd := "---\ndescription: DCF 与可比公司估值分析\nagent: plan\nargument-hint: <股票代码或公司名>\n---\n请对 $ARGUMENTS 进行完整估值建模分析。"
	if err := os.WriteFile(filepath.Join(ocCmdDir, "估值.md"), []byte(valuationCmd), 0o644); err != nil {
		t.Fatal(err)
	}

	extraCfg := map[string]any{
		"command": map[string]any{
			"bench": map[string]any{
				"description": "Run Go benchmark analysis",
				"template":    "Benchmark package $1",
				"agent":       "debug",
			},
		},
	}

	cmds := LoadCommands(wsDir, extraCfg)
	names := make(map[string]SlashCommandSpec, len(cmds))
	for _, c := range cmds {
		names[c.Name] = c
	}
	for _, want := range []string{"compact", "init", "review", "fe:audit", "changelog", "估值", "bench"} {
		if _, ok := names[want]; !ok {
			t.Fatalf("missing expected command %q in %+v", want, cmds)
		}
	}

	// /compact must not be expanded as an LLM template prompt
	if _, ok := ExpandSlashCommand("/compact", wsDir, extraCfg); ok {
		t.Fatal("expected /compact to stay unexpanded so client compaction handles it")
	}

	// Built-in /review switches to plan mode and interpolates $ARGUMENTS
	revExp, ok := ExpandSlashCommand("/review internal/runner/prompt_submit.go", wsDir, extraCfg)
	if !ok {
		t.Fatal("expected /review to expand")
	}
	if revExp.Agent != "plan" || !strings.Contains(revExp.RenderedPrompt, "internal/runner/prompt_submit.go") {
		t.Fatalf("unexpected /review expansion: %+v", revExp)
	}

	// Built-in /init targets build mode and AGENTS.md
	initExp, ok := ExpandSlashCommand("/init", wsDir, extraCfg)
	if !ok || initExp.Agent != "build" || !strings.Contains(initExp.RenderedPrompt, "AGENTS.md") {
		t.Fatalf("unexpected /init expansion: ok=%v exp=%+v", ok, initExp)
	}

	// Nested /fe/audit alias matches fe:audit and expands $1, $2, and $ARGUMENTS
	feExp, ok := ExpandSlashCommand(`/fe/audit "Composer.tsx" AA`, wsDir, extraCfg)
	if !ok {
		t.Fatal("expected /fe/audit alias to match fe:audit")
	}
	if feExp.Agent != "plan" || feExp.Model != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("unexpected agent/model override: %+v", feExp)
	}
	if !strings.Contains(feExp.RenderedPrompt, "Audit component Composer.tsx against WCAG AA.") ||
		!strings.Contains(feExp.RenderedPrompt, `Full raw args: "Composer.tsx" AA`) {
		t.Fatalf("unexpected positional/ARGUMENTS expansion:\n%s", feExp.RenderedPrompt)
	}

	// Command without placeholders appends trailing Arguments: ...
	clExp, ok := ExpandSlashCommand("/changelog v1.2.0..HEAD", wsDir, extraCfg)
	if !ok || !strings.HasSuffix(clExp.RenderedPrompt, "Arguments: v1.2.0..HEAD") {
		t.Fatalf("expected trailing Arguments suffix, got:\n%s", clExp.RenderedPrompt)
	}

	// OpenCode CJK command (.opencode/commands/估值.md) expands $ARGUMENTS and agent override
	valExp, ok := ExpandSlashCommand("/估值 腾讯控股 00700.HK", wsDir, extraCfg)
	if !ok || valExp.Agent != "plan" || !strings.Contains(valExp.RenderedPrompt, "请对 腾讯控股 00700.HK 进行完整估值建模分析。") {
		t.Fatalf("unexpected /估值 expansion: ok=%v exp=%+v", ok, valExp)
	}
}

func TestSlashCommandTemplateFileInliningAndRejections(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	wsDir := t.TempDir()
	outsideDir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(wsDir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "docs", "rubric.md"), []byte("1. Check FCF yield\n2. Compare EV/EBITDA"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "docs", "multiples.md"), []byte("SaaS median EV/Sales = 6.5x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wsDir, ".hoxi", "skills", "dcf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, ".hoxi", "skills", "dcf", "SKILL.md"), []byte("Use 10-year DCF terminal multiple."), 0o644); err != nil {
		t.Fatal(err)
	}
	// Sensitive & boundary files that MUST NEVER be auto-inlined
	if err := os.WriteFile(filepath.Join(wsDir, ".env"), []byte("OPENAI_API_KEY=sk-live-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "docs", "db_secret.txt"), []byte("TOP_SECRET_PASS"), 0o600); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outsideDir, "outside.md")
	if err := os.WriteFile(outsideFile, []byte("OUTSIDE_WORKSPACE_DATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink(outsideFile, filepath.Join(wsDir, "docs", "escape.md"))
	_ = os.Symlink(filepath.Join(wsDir, ".env"), filepath.Join(wsDir, "docs", "env_alias.txt"))

	ocCmdDir := filepath.Join(wsDir, ".opencode", "commands")
	if err := os.MkdirAll(ocCmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmdBody := strings.Join([]string{
		"---",
		"description: Deep valuation with rubric",
		"subtask: true",
		"---",
		"Follow @docs/rubric.md、@docs/multiples.md and @.hoxi/skills/dcf/SKILL.md.",
		"Refuse @.env, @docs/db_secret.txt, @docs/escape.md, @docs/env_alias.txt, and @../outside.md.",
		"Target: $ARGUMENTS",
	}, "\n")
	if err := os.WriteFile(filepath.Join(ocCmdDir, "deep-val.md"), []byte(cmdBody), 0o644); err != nil {
		t.Fatal(err)
	}

	exp, ok := ExpandSlashCommand("/deep-val AAPL @.env @docs/db_secret.txt", wsDir, nil)
	if !ok {
		t.Fatal("expected /deep-val to expand")
	}
	if !exp.Subtask {
		t.Fatal("expected subtask: true frontmatter to propagate to ExpandedCommand.Subtask")
	}
	if !strings.Contains(exp.RenderedPrompt, `<file path="docs/rubric.md">`) ||
		!strings.Contains(exp.RenderedPrompt, "1. Check FCF yield") {
		t.Fatalf("expected @docs/rubric.md to be inlined, got:\n%s", exp.RenderedPrompt)
	}
	if !strings.Contains(exp.RenderedPrompt, `<file path="docs/multiples.md">`) ||
		!strings.Contains(exp.RenderedPrompt, "SaaS median EV/Sales = 6.5x") {
		t.Fatalf("expected CJK-comma-separated @docs/multiples.md to be inlined, got:\n%s", exp.RenderedPrompt)
	}
	if !strings.Contains(exp.RenderedPrompt, `<file path=".hoxi/skills/dcf/SKILL.md">`) {
		t.Fatalf("expected @.hoxi/skills/dcf/SKILL.md to be inlined, got:\n%s", exp.RenderedPrompt)
	}
	for _, forbidden := range []string{"sk-live-secret", "TOP_SECRET_PASS", "OUTSIDE_WORKSPACE_DATA"} {
		if strings.Contains(exp.RenderedPrompt, forbidden) {
			t.Fatalf("security violation: forbidden content %q leaked into rendered prompt:\n%s", forbidden, exp.RenderedPrompt)
		}
	}
}

func TestLoadAgentsAndTaskPermissions(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	wsDir := t.TempDir()

	// .claude/agents defaults to mode="subagent" and ReadOnly when tools lack write/edit/bash
	claudeAgents := filepath.Join(wsDir, ".claude", "agents")
	if err := os.MkdirAll(claudeAgents, 0o755); err != nil {
		t.Fatal(err)
	}
	secAgent := "---\nname: sec-audit\ndescription: Security auditor\ntools: Read, Grep, Glob\nsteps: 7\n---\nInspect auth boundaries carefully."
	if err := os.WriteFile(filepath.Join(claudeAgents, "sec-audit.md"), []byte(secAgent), 0o644); err != nil {
		t.Fatal(err)
	}

	// .opencode/agents defaults to mode="all" and honors permission.edit: deny
	ocAgents := filepath.Join(wsDir, ".opencode", "agents")
	if err := os.MkdirAll(ocAgents, 0o755); err != nil {
		t.Fatal(err)
	}
	archAgent := "---\ndescription: System architect\nmodel: openrouter/anthropic/claude-opus-4\npermission:\n  edit: deny\n---\nDesign clean modular boundaries."
	if err := os.WriteFile(filepath.Join(ocAgents, "architect.md"), []byte(archAgent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgDoc := map[string]any{
		"agent": map[string]any{
			"architect": map[string]any{
				"permission": map[string]any{
					"task": map[string]any{
						"*":       "deny",
						"explore": "allow",
						"sec-*":   "allow",
					},
				},
			},
			"scout": map[string]any{
				"disable": true,
			},
		},
	}

	active, byKey := LoadAgents(wsDir, cfgDoc)
	for _, disabled := range active {
		if disabled.Name == "scout" {
			t.Fatal("expected disabled agent 'scout' to be omitted from active list")
		}
	}

	sec, ok := byKey["sec-audit"]
	if !ok || sec.Mode != "subagent" || !sec.ReadOnly || sec.Steps != 7 {
		t.Fatalf("unexpected sec-audit spec: ok=%v spec=%+v", ok, sec)
	}

	arch, ok := byKey["architect"]
	if !ok || arch.Mode != "all" || !arch.ReadOnly {
		t.Fatalf("unexpected architect spec: ok=%v spec=%+v", ok, arch)
	}
	prov, mod := SplitModelRef(arch.Model)
	if prov != "openrouter" || mod != "anthropic/claude-opus-4" {
		t.Fatalf("unexpected SplitModelRef(%q) = (%q, %q)", arch.Model, prov, mod)
	}

	if !IsSubagentAllowed("architect", "explore", byKey, cfgDoc) {
		t.Fatal("expected architect to allow explore")
	}
	if !IsSubagentAllowed("architect", "sec-audit", byKey, cfgDoc) {
		t.Fatal("expected architect wildcard sec-* to allow sec-audit")
	}
	if IsSubagentAllowed("architect", "general", byKey, cfgDoc) {
		t.Fatal("expected architect '*'=deny to block general subagent")
	}
	if IsSubagentAllowed("build", "scout", byKey, cfgDoc) {
		t.Fatal("expected disabled scout subagent to be blocked")
	}
}
