package codeindex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMarkDirtyImmediatelyRefreshesWithinCacheTTL(t *testing.T) {
	ws := t.TempDir()
	filePath := filepath.Join(ws, "app.go")
	if err := os.WriteFile(filePath, []byte("package app\n\nfunc LegacyAlphaFoo() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewService(t.TempDir())
	// Freeze wall clock so TTL (60s) never expires naturally.
	svc.now = func() int64 { return 1_000_000 }

	res1, err := svc.Search(ws, "LegacyAlphaFoo", 10)
	if err != nil || len(res1.Results) == 0 || res1.Results[0].Symbol != "LegacyAlphaFoo" {
		t.Fatalf("expected initial index to find LegacyAlphaFoo, err=%v results=%+v", err, res1.Results)
	}

	// Mutate file on disk without advancing clock.
	if err := os.WriteFile(filePath, []byte("package app\n\nfunc BrandNewOmegaBar() int { return 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Without MarkDirty, cached snapshot within 60s TTL is served.
	stale, err := svc.Search(ws, "BrandNewOmegaBar", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale.Results) != 0 {
		t.Fatalf("expected cached index before MarkDirty, got %+v", stale.Results)
	}

	// MarkDirty forces incremental rebuild on the very next query even at the exact same timestamp.
	svc.MarkDirty(ws)
	fresh, err := svc.Search(ws, "BrandNewOmegaBar", 10)
	if err != nil || len(fresh.Results) == 0 || fresh.Results[0].Symbol != "BrandNewOmegaBar" {
		t.Fatalf("expected MarkDirty to surface BrandNewOmegaBar immediately, err=%v results=%+v", err, fresh.Results)
	}
	// Also verify RepoMap sees the updated symbol immediately.
	rmap, err := svc.Map(ws, "", "", 10)
	if err != nil || len(rmap.Entries) == 0 {
		t.Fatalf("Map failed after MarkDirty: err=%v entries=%+v", err, rmap.Entries)
	}
	foundNew := false
	for _, sym := range rmap.Entries[0].Symbols {
		if sym.Symbol == "BrandNewOmegaBar" {
			foundNew = true
		}
	}
	if !foundNew {
		t.Fatalf("expected RepoMap after MarkDirty to include BrandNewOmegaBar, got %+v", rmap.Entries[0].Symbols)
	}
}
