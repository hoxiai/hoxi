package runner

import (
	"strings"
	"testing"
)

func TestSplitTurnDirectiveKeepsSystemPrefixStable(t *testing.T) {
	stableBlock := "<hoxi_work_store_profile>store</hoxi_work_store_profile>\n\nAlways answer in Chinese."
	withDirective := stableBlock + "\n\n<hoxi_turn_directive>\n[Modality Router: Image]\n- Invoke hoxi_image.\n</hoxi_turn_directive>"
	stable, directive := splitTurnDirective(withDirective)
	plain, none := splitTurnDirective(stableBlock)
	if stable != plain || none != "" {
		t.Fatalf("system prefix must be identical with and without a turn directive:\n%q\n%q", stable, plain)
	}
	if directive != "[Modality Router: Image]\n- Invoke hoxi_image." {
		t.Fatalf("unexpected directive %q", directive)
	}
	got := withTurnDirective(directive, "draw a cat")
	if !strings.HasPrefix(got, "<hoxi_turn_directive>\n[Modality Router: Image]") || !strings.HasSuffix(got, "\n\ndraw a cat") {
		t.Fatalf("directive must travel with the user prompt: %q", got)
	}
	if withTurnDirective("", "draw a cat") != "draw a cat" {
		t.Fatal("prompts without a directive must be unchanged")
	}
}

func TestSplitTurnDirectiveIgnoresUnclosedOrEmptySections(t *testing.T) {
	unclosed := "base rule\n\n<hoxi_turn_directive>\nnever closed"
	if stable, directive := splitTurnDirective(unclosed); stable != unclosed || directive != "" {
		t.Fatalf("an unclosed tag must be left untouched, got %q / %q", stable, directive)
	}
	if stable, directive := splitTurnDirective("base\n\n<hoxi_turn_directive>  </hoxi_turn_directive>"); stable != "base" || directive != "" {
		t.Fatalf("empty directive must be dropped without leaking: %q / %q", stable, directive)
	}
	if got := joinPromptSections("sys", "dir"); got != "sys\n\ndir" || joinPromptSections("", "dir") != "dir" || joinPromptSections("sys", " ") != "sys" {
		t.Fatalf("joinPromptSections = %q", got)
	}
}
