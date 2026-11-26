package memstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInjectedProjectMemoryIsCappedWithExplicitNotice(t *testing.T) {
	project := t.TempDir()
	memDir := filepath.Join(project, ".hoxi", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	head := "HEAD_SENTINEL project rule\n"
	tail := "TAIL_SENTINEL beyond the cap\n"
	doc := head + strings.Repeat("项目约束：保持接口稳定。\n", 4000) + tail // ~148 KB
	if err := os.WriteFile(filepath.Join(memDir, "project.md"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	projBlock, _ := NativeMemoryBlocks(SessionRecord{ID: "ses_cap", ProjectDir: project}, false)
	if !strings.Contains(projBlock, "HEAD_SENTINEL") || strings.Contains(projBlock, "TAIL_SENTINEL") {
		t.Fatalf("expected head kept and tail dropped, got %d bytes", len(projBlock))
	}
	if !strings.Contains(projBlock, "Project memory truncated") {
		t.Fatal("truncation must be announced so the agent can read the remainder")
	}
	if len(projBlock) > MaxInjectedProjectMemoryBytes+512 {
		t.Fatalf("injected project memory exceeds cap: %d bytes", len(projBlock))
	}

	small := "small project memory\n"
	if err := os.WriteFile(filepath.Join(memDir, "project.md"), []byte(small), 0o600); err != nil {
		t.Fatal(err)
	}
	smallBlock, _ := NativeMemoryBlocks(SessionRecord{ID: "ses_cap", ProjectDir: project}, false)
	if !strings.Contains(smallBlock, "small project memory") || strings.Contains(smallBlock, "truncated") {
		t.Fatalf("documents under the cap must be injected unchanged: %q", smallBlock)
	}
}
