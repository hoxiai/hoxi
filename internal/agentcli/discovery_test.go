package agentcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodeBuddyAndQoderModelDiscovery(t *testing.T) {
	if mult, free := parseCodeBuddyCredits("x0.00 credits"); !free || mult != "0x" {
		t.Fatalf("expected x0.00 credits to be free 0x, got %q / %v", mult, free)
	}
	if mult, free := parseCodeBuddyCredits("x0.29 credits"); free || mult != "0.29x" {
		t.Fatalf("expected x0.29 credits to be 0.29x, got %q / %v", mult, free)
	}
	cbBuilt := buildCodeBuddyModelItems("", defaultCodeBuddyModelSlugs())
	var sawCBFreeHy3, sawCBFreeDS, sawCBHy4 bool
	for _, it := range cbBuilt {
		if it.ID == "hy3" && it.Free && it.Multiplier == "0x" && it.ContextLimit == 192000 {
			sawCBFreeHy3 = true
		}
		if it.ID == "deepseek-v4.1-flash" && it.Free && it.Multiplier == "0x" && it.ContextLimit == 1000000 {
			sawCBFreeDS = true
		}
		if it.ID == "hy4-preview" && !it.Free && it.Multiplier == "0.29x" && it.InputCost > 0 {
			sawCBHy4 = true
		}
	}
	if !sawCBFreeHy3 || !sawCBFreeDS || !sawCBHy4 {
		t.Fatalf("expected CodeBuddy free (0x) and multiplier metadata, got: %+v", cbBuilt)
	}

	cbModels := ParseCodeBuddyModelsOutput("  --model <model>  Currently supported: (default-model, hy4-preview, glm-5.3, gpt-6-astra)\n")
	if len(cbModels) != 4 || cbModels[1] != "hy4-preview" || cbModels[3] != "gpt-6-astra" {
		t.Fatalf("unexpected CodeBuddy parsed models: %v", cbModels)
	}
	tmpDir := t.TempDir()
	intlBin := filepath.Join(tmpDir, "qodercli")
	cnBin := filepath.Join(tmpDir, "qoderclicn")
	_ = os.WriteFile(intlBin, []byte("#!/bin/sh\nprintf 'MODEL\\nAuto\\nclaude-sonnet-4-6\\n'\n"), 0o755)
	_ = os.WriteFile(cnBin, []byte("#!/bin/sh\nprintf 'MODEL\\nAuto\\nQwen3.8-Max\\nQwen3.8-Flash\\n'\n"), 0o755)
	t.Setenv("QODER_BIN", intlBin)
	t.Setenv("QODER_CN_BIN", cnBin)

	items := DiscoverQoderModelsWithRefresh(true)
	var sawCN, sawCNFree, sawIntl, sawHeader bool
	for _, it := range items {
		if strings.EqualFold(it.ID, "model") || strings.HasSuffix(strings.ToLower(it.ID), ":model") {
			sawHeader = true
		}
		if it.ID == "cn:Qwen3.8-Max" && it.Multiplier == "0.5x" && it.InputCost > 0 {
			sawCN = true
		}
		if it.ID == "cn:Qwen3.8-Flash" && it.Free && it.Multiplier == "0x" {
			sawCNFree = true
		}
		if it.ID == "intl:claude-sonnet-4-6" && it.Multiplier == "1.1x" {
			sawIntl = true
		}
	}
	if sawHeader || !sawCN || !sawCNFree || !sawIntl {
		t.Fatalf("expected both CN (with free 0x & 0.5x) and Intl Qoder models without MODEL header, got: %+v", items)
	}

	resolvedBin, cleanModel := ResolveQoderBinaryForModel("cn:Qwen3.8-Max")
	if resolvedBin != cnBin || cleanModel != "Qwen3.8-Max" {
		t.Fatalf("expected CN bin %q and clean model Qwen3.8-Max, got %q / %q", cnBin, resolvedBin, cleanModel)
	}
	resolvedIntl, cleanIntl := ResolveQoderBinaryForModel("intl:claude-sonnet-4-6")
	if resolvedIntl != intlBin || cleanIntl != "claude-sonnet-4-6" {
		t.Fatalf("expected Intl bin %q and clean model claude-sonnet-4-6, got %q / %q", intlBin, resolvedIntl, cleanIntl)
	}

	if got := FormatClaudeModelName("claude-sonnet-4-6-20251001-thinking"); got != "Claude Sonnet 4.6" {
		t.Fatalf("FormatClaudeModelName with date before suffix = %q, want Claude Sonnet 4.6", got)
	}
	if got := FormatClaudeModelName("claude-fable-6-1"); got != "Claude Fable 6.1" {
		t.Fatalf("FormatClaudeModelName(claude-fable-6-1) = %q, want Claude Fable 6.1", got)
	}
}
