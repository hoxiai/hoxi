package gateway

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestManifestValidationContract(t *testing.T) {
	validRaw := []byte(`{
		"schemaVersion": "hoxi.extension/v1",
		"id": "ops-checker",
		"name": "Ops Checker",
		"version": "1.0.0",
		"permissions": ["tools:register", "panels:register", "network:local"],
		"tools": [
			{
				"name": "ops_check",
				"description": "Verify local service health",
				"readOnly": true,
				"startupBudgetMs": 2000,
				"endpoint": "http://localhost:9911/health",
				"parameters": {
					"type": "object",
					"properties": {
						"target": { "type": "string" }
					},
					"required": ["target"]
				}
			}
		],
		"panels": [
			{
				"id": "ops-panel",
				"title": "Ops",
				"slot": "inspector",
				"entry": "ui/panel.js"
			}
		]
	}`)
	parsed, err := ValidateExtensionManifest(validRaw)
	if err != nil {
		t.Fatalf("ValidateExtensionManifest failed: %v", err)
	}
	if parsed.ID != "ops-checker" || len(parsed.Tools) != 1 || parsed.Tools[0].StartupBudgetMs != 2000 {
		t.Fatalf("unexpected parsed manifest: %+v", parsed)
	}

	for _, bad := range []struct {
		raw  string
		want string
	}{
		{
			raw:  `{"schemaVersion":"hoxi.extension/v1","id":"x1","name":"X","version":"0.1.0","permissions":["tools:register","network:local"],"tools":[{"name":"hoxi_internal","description":"d","endpoint":"http://localhost:8080/x","parameters":{"type":"object","properties":{}}}]}`,
			want: "reserved",
		},
		{
			raw:  `{"schemaVersion":"hoxi.extension/v1","id":"x2","name":"X","version":"0.1.0","permissions":["network:local"],"tools":[{"name":"probe_one","description":"d","endpoint":"http://localhost:8080/x","parameters":{"type":"object","properties":{}}}]}`,
			want: "tools:register",
		},
		{
			raw:  `{"schemaVersion":"hoxi.extension/v1","id":"x3","name":"X","version":"0.1.0","extra":1}`,
			want: "unknown field",
		},
	} {
		if _, err := ValidateExtensionManifest([]byte(bad.raw)); err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Fatalf("expected error containing %q, got %v", bad.want, err)
		}
	}
	_ = filepath.Clean(".")
}
