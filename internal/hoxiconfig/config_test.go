package hoxiconfig

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func putConfig(t *testing.T, root, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, FileName), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadJSONCAndNoWrite(t *testing.T) {
	root := t.TempDir()
	if got, err := Load(root); got != nil || err != nil {
		t.Fatalf("missing: %v %v", got, err)
	}
	raw := `{
 // Only metadata: no schema is fetched.
 "$schema": "https://example.test/schema.json",
 "version": 1,
 "permission": {"bash":"allow", "edit":"ask",},
 "skills": {"paths":[".hoxi/skills", "custom",]},
 "mcp": {"demo":{"type":"local","command":["node","./launch.mjs",""],"enabled":true}},
}`
	putConfig(t, root, raw)
	doc, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if ToolAction(doc, "write") != "ask" || ToolAction(doc, "bash") != "allow" {
		t.Fatal(doc)
	}
	want := []string{filepath.Join(root, ".hoxi", "skills"), filepath.Join(root, "custom")}
	if got := SkillPaths(root, doc); !reflect.DeepEqual(got, want) {
		t.Fatalf("paths: %v", got)
	}
	after, _ := os.ReadFile(filepath.Join(root, FileName))
	if string(after) != raw {
		t.Fatal("read modified project configuration")
	}
}

func TestLoadRejectsInvalidWithoutRenaming(t *testing.T) {
	for _, raw := range []string{
		`null`, `[]`, `{"version":2}`, `{"version":{}}`, `{"permission":{"bash":true}}`,
		`{"mcp":{"x":{"type":{}}}}`, `{"mcp":{" x ":{"enabled":false}}}`,
		`{"mcp":{"x":{"enabled":false},"X":{"enabled":false}}}`,
		`{"permission":{"bash":{"*":"allow"}}}`, `{"permission":{"bas":"allow"}}`,
		`{"provider":{}}`, `{"plugins":[]}`, `{"skills":{"paths":["../other"]}}`,
		`{"skills":{"paths":[7]}}`, `{"skills":{"urls":[]}}`,
		`{"mcp":{"x":{"type":"local","command":[]}}}`,
		`{"mcp":{"x":{"type":"local","command":[7]}}}`,
		`{"mcp":{"x":{"type":"remote","url":"file:///tmp/x"}}}`,
		`{"mcp":{"x":{"type":"remote","url":"https://user:pass@example.test"}}}`,
		`{"mcp":{"x":{"type":"remote","url":"https://example.test","headers":{"Authorization":"secret"}}}}`,
		`{"mcp":{"x":{"type":"local","command":["node"],"enabled":"false"}}}`,
		`{"mcp":{"x":{"type":"local","command":["node"],"environment":{"A":3}}}}`,
		`{"version":1/*unterminated`, `{"version":1/*comment*/1}`, `{}/*unterminated`,
	} {
		t.Run(raw, func(t *testing.T) {
			root := t.TempDir()
			putConfig(t, root, raw)
			if _, err := Load(root); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("accepted %s: %v", raw, err)
			}
			files, _ := os.ReadDir(root)
			if len(files) != 1 || files[0].Name() != FileName {
				t.Fatal("invalid shared file renamed or altered")
			}
		})
	}
}

func TestLoadRejectsOversizeDirectoryAndSymlink(t *testing.T) {
	root := t.TempDir()
	putConfig(t, root, strings.Repeat(" ", MaxConfigBytes+1))
	if _, err := Load(root); err == nil {
		t.Fatal("oversize accepted")
	}
	other := t.TempDir()
	if err := os.Mkdir(filepath.Join(other, FileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(other); err == nil {
		t.Fatal("directory accepted")
	}
	linkRoot := t.TempDir()
	if err := os.Symlink(filepath.Join(root, FileName), filepath.Join(linkRoot, FileName)); err != nil {
		t.Skip(err)
	}
	if _, err := Load(linkRoot); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestOverlayIndependentAndDisabledServers(t *testing.T) {
	base := map[string]any{"mcp": map[string]any{"demo": map[string]any{"type": "local", "command": []any{"node"}, "environment": map[string]any{"TOKEN": "local"}}}}
	project := map[string]any{"$schema": "shared", "version": float64(1), "mcp": map[string]any{"demo": map[string]any{"enabled": false}}}
	got := Overlay(base, project)
	server := got["mcp"].(map[string]any)["demo"].(map[string]any)
	if server["enabled"] != false || server["type"] != "local" {
		t.Fatal(got)
	}
	server["command"].([]any)[0] = "changed"
	if base["mcp"].(map[string]any)["demo"].(map[string]any)["command"].([]any)[0] != "node" {
		t.Fatal("mutated input")
	}
	if got["version"] != nil || got["$schema"] != nil {
		t.Fatal("metadata leaked")
	}
}
