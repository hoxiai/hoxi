package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepContinuationAndOversizedLines(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"a.txt": strings.Repeat("z", (1<<20)+100) + "\na marker\n", "b.txt": strings.Repeat("b marker\n", 179), "large.txt": strings.Repeat("z", (2<<20)+1)} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(offset int) ToolOutput {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"pattern": "marker", "offset": offset})
		out, err := newToolRegistry().Execute(context.Background(), ToolContext{WorktreeDir: dir}, ToolCall{Name: "grep", Input: b}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := run(0)
	if !strings.HasPrefix(first.Output, "a.txt:2: a marker") || !strings.Contains(first.Output, "b.txt:149: b marker") || first.Metadata["nextOffset"] != 150 {
		t.Fatalf("unexpected first page: %v %s", first.Metadata, first.Output)
	}
	last := run(150)
	if !strings.HasPrefix(last.Output, "b.txt:150: b marker") || strings.Count(last.Output, "b marker") != 30 || last.Metadata["nextOffset"] != nil || last.Metadata["skippedFiles"] != 1 {
		t.Fatalf("unexpected final page: %+v", last)
	}
}
