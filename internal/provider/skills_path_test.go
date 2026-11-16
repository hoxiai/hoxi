package provider

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func readSkillsValues(t *testing.T, path string) []string {
	t.Helper()
	doc := readJSONMapFile(path)
	if doc == nil {
		t.Fatalf("read %s: empty or invalid", path)
	}
	var values []string
	switch layer := doc["skills"].(type) {
	case []interface{}:
		for _, item := range layer {
			if value, ok := item.(string); ok {
				values = append(values, value)
			}
		}
	case map[string]interface{}:
		for _, key := range []string{"paths", "urls"} {
			raw, _ := layer[key].([]interface{})
			for _, item := range raw {
				if value, ok := item.(string); ok {
					values = append(values, value)
				}
			}
		}
	default:
		t.Fatalf("%s skills = %#v, want array or object", path, doc["skills"])
	}
	return values
}

func TestEnsureHoxiModelsConfigRegistersHoxiSkills(t *testing.T) {
	home, workspace := setupIsolatedEnv(t)
	userPath := filepath.Join(home, ".hoxi", "models.json")
	workspacePath := filepath.Join(workspace, ".hoxi", "models.json")
	wantPath := filepath.Join(home, ".hoxi", "skills")
	customPath := filepath.Join(home, "custom-skills")
	customURL := "https://example.test/.well-known/skills/"

	if err := os.MkdirAll(filepath.Dir(userPath), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"skills":{"paths":["` + customPath + `"],"urls":["` + customURL + `"]}}`
	if err := os.WriteFile(userPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	EnsureHoxiModelsConfig(workspace)

	for _, path := range []string{userPath, workspacePath} {
		values := readSkillsValues(t, path)
		for _, want := range []string{wantPath, customPath, customURL} {
			if !slices.Contains(values, want) {
				t.Fatalf("%s values = %v, want %q", path, values, want)
			}
		}
	}

	EnsureHoxiModelsConfig(workspace)
	finalValues := readSkillsValues(t, userPath)
	count := 0
	for _, value := range finalValues {
		if value == wantPath {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("skills path not idempotent: %v", finalValues)
	}
}

func TestEnsureHoxiModelsConfigKeepsArraySkills(t *testing.T) {
	home, workspace := setupIsolatedEnv(t)
	userPath := filepath.Join(home, ".hoxi", "models.json")
	customPath := filepath.Join(home, "array-skill")

	if err := os.MkdirAll(filepath.Dir(userPath), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"skills":["` + customPath + `"]}`
	if err := os.WriteFile(userPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	EnsureHoxiModelsConfig(workspace)

	values := readSkillsValues(t, userPath)
	if !slices.Contains(values, customPath) {
		t.Fatalf("array-form user skill dropped: %v", values)
	}
	if !slices.Contains(values, filepath.Join(home, ".hoxi", "skills")) {
		t.Fatalf("hoxi skills missing: %v", values)
	}
}
