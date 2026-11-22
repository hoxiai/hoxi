package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/agent"
)

func TestNativeHostCodeIndexAndImageTools(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	wsDir := t.TempDir()
	srcCode := `package sample

func HelperAdd(a, b int) int {
	return a + b
}

func ComputeTotal(items []int) int {
	sum := 0
	for _, v := range items {
		sum = HelperAdd(sum, v)
	}
	return sum
}
`
	testCode := `package sample

import "testing"

func TestComputeTotal(t *testing.T) {
	if got := ComputeTotal([]int{2, 3}); got != 5 {
		t.Fatalf("got %d", got)
	}
}
`
	if err := os.WriteFile(filepath.Join(wsDir, "calc.go"), []byte(srcCode), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "calc_test.go"), []byte(testCode), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, ".env"), []byte("API_SECRET=topsecret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	g := New(0, "", wsDir, nil, nil)
	defer g.CloseBrowser()

	tc := agent.ToolContext{SessionID: "ses_ci_1", WorktreeDir: wsDir}

	// 1. refresh
	refRes, err := g.invokeNativeHostTool(context.Background(), tc, "hoxi_code_index", json.RawMessage(`{"action":"refresh"}`))
	if err != nil || !strings.Contains(refRes.Output, "Index refreshed") {
		t.Fatalf("refresh failed: err=%v out=%q", err, refRes.Output)
	}

	// 2. context (1-call surgical symbol bundle: source + callees + callers + tests)
	ctxRes, err := g.invokeNativeHostTool(context.Background(), tc, "hoxi_code_index", json.RawMessage(`{"action":"context","symbol":"ComputeTotal"}`))
	if err != nil {
		t.Fatalf("context failed: %v", err)
	}
	for _, want := range []string{"# calc.go:", "HelperAdd", "TestComputeTotal", "Callees (", "Tests ("} {
		if !strings.Contains(ctxRes.Output, want) {
			t.Fatalf("context output missing %q:\n%s", want, ctxRes.Output)
		}
	}

	// 3. inspect with line range + callees + impact + search
	inspRes, err := g.invokeNativeHostTool(context.Background(), tc, "hoxi_code_index", json.RawMessage(`{"action":"inspect","path":"calc.go","startLine":3,"endLine":5}`))
	if err != nil || !strings.Contains(inspRes.Output, "func HelperAdd") {
		t.Fatalf("inspect failed: err=%v out=%q", err, inspRes.Output)
	}
	calleesRes, err := g.invokeNativeHostTool(context.Background(), tc, "hoxi_code_index", json.RawMessage(`{"action":"callees","symbol":"ComputeTotal"}`))
	if err != nil || !strings.Contains(calleesRes.Output, "HelperAdd") {
		t.Fatalf("callees failed: err=%v out=%q", err, calleesRes.Output)
	}
	impRes, err := g.invokeNativeHostTool(context.Background(), tc, "hoxi_code_index", json.RawMessage(`{"action":"impact","symbol":"HelperAdd"}`))
	if err != nil || !strings.Contains(impRes.Output, "ComputeTotal") {
		t.Fatalf("impact failed: err=%v out=%q", err, impRes.Output)
	}
	srchRes, err := g.invokeNativeHostTool(context.Background(), tc, "hoxi_code_index", json.RawMessage(`{"action":"search","query":"ComputeTotal"}`))
	if err != nil || !strings.Contains(srchRes.Output, "calc.go") {
		t.Fatalf("search failed: err=%v out=%q", err, srchRes.Output)
	}

	// 4. Refusal paths: secret .env, protected .git, and .. traversal MUST be rejected
	for _, badPath := range []string{".env", ".git/config", "../escape.go"} {
		payload, _ := json.Marshal(map[string]any{"action": "inspect", "path": badPath})
		if _, err := g.invokeNativeHostTool(context.Background(), tc, "hoxi_code_index", payload); err == nil {
			t.Fatalf("expected hoxi_code_index to refuse sensitive/escaping path %q", badPath)
		}
	}

	// 5. hoxi_image / hoxi_video / hoxi_speech false-success regression + structured missingCapability metadata!
	for _, tcMedia := range []struct {
		tool string
		body string
		cap  string
	}{
		{"hoxi_image", `{"prompt":"test icon"}`, "image"},
		{"hoxi_video", `{"prompt":"test video"}`, "video"},
		{"hoxi_speech", `{"text":"hello"}`, "speech"},
	} {
		res, err := g.invokeNativeHostTool(context.Background(), tc, tcMedia.tool, json.RawMessage(tcMedia.body))
		if err == nil {
			t.Fatalf("expected %s without configured provider/model to fail instead of returning false success", tcMedia.tool)
		}
		if res.Metadata == nil || res.Metadata["missingCapability"] != tcMedia.cap {
			t.Fatalf("expected %s error metadata to carry missingCapability=%q, got %+v", tcMedia.tool, tcMedia.cap, res.Metadata)
		}
	}
}
