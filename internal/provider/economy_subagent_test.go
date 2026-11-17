package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveEconomyModelStaysWithinSameProviderAndHonorsSelection(t *testing.T) {
	hoxiHome := t.TempDir()
	t.Setenv("HOXI_HOME", hoxiHome)
	t.Setenv("HOME", t.TempDir())
	wsDir := t.TempDir()

	modelsJSON := `{
  "provider": {
    "anthropic": {
      "options": {
        "baseURL": "https://api.anthropic.com",
        "apiKey": "sk-ant-test-key"
      },
      "models": {
        "claude-opus-4-6": { "name": "Claude Opus 4.6" },
        "claude-sonnet-4-6": { "name": "Claude Sonnet 4.6" },
        "claude-haiku-4-5": { "name": "Claude Haiku 4.5", "family": "claude-haiku" },
        "claude-3-haiku": { "name": "Claude 3 Haiku", "family": "claude-haiku" }
      }
    },
    "google": {
      "options": {
        "baseURL": "https://generativelanguage.googleapis.com/v1beta",
        "apiKey": "goog-test-key"
      },
      "models": {
        "gemini-2.5-pro": { "name": "Gemini 2.5 Pro" },
        "gemini-2.5-flash": { "name": "Gemini 2.5 Flash", "family": "gemini-flash" },
        "gemini-2.5-flash-lite": { "name": "Gemini 2.5 Flash Lite", "family": "gemini-flash" },
        "imagen-3.0-flash": { "name": "Imagen Flash", "tool_call": false }
      }
    },
    "solo-frontier": {
      "options": {
        "baseURL": "https://api.solo.example/v1",
        "apiKey": "sk-solo-key"
      },
      "models": {
        "deepseek-v3": { "name": "DeepSeek V3" }
      }
    }
  }
}`
	if err := os.WriteFile(filepath.Join(hoxiHome, "models.json"), []byte(modelsJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	svc := NewService(0, "", wsDir, nil)

	// 1. Anthropic picks latest Haiku within anthropic.
	gotAnth, ok := svc.ResolveEconomyModel("anthropic", "claude-opus-4-6")
	if !ok || gotAnth != "claude-haiku-4-5" {
		t.Fatalf("expected anthropic economy model claude-haiku-4-5, got %q (ok=%v)", gotAnth, ok)
	}

	// 2. Google picks standard tier-1 gemini-2.5-flash ahead of flash-lite and skips non-tool imagen.
	gotGoog, ok := svc.ResolveEconomyModel("google", "gemini-2.5-pro")
	if !ok || gotGoog != "gemini-2.5-flash" {
		t.Fatalf("expected google economy model gemini-2.5-flash, got %q (ok=%v)", gotGoog, ok)
	}

	// 3. Strict Rule #11 isolation: solo-frontier has no economy model and MUST NOT cross to google/anthropic.
	gotSolo, ok := svc.ResolveEconomyModel("solo-frontier", "deepseek-v3")
	if ok || gotSolo != "" {
		t.Fatalf("expected no cross-provider fallback for solo-frontier, got %q (ok=%v)", gotSolo, ok)
	}

	// 4. Already using an economy model keeps the current model ID.
	gotSame, ok := svc.ResolveEconomyModel("google", "gemini-2.5-flash-lite")
	if !ok || gotSame != "gemini-2.5-flash-lite" {
		t.Fatalf("expected existing economy model to be preserved, got %q (ok=%v)", gotSame, ok)
	}
}
