package orchestration

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "orchestration")
	store := NewStore(dir)
	plan := Plan{
		ID:        "plan_test_roundtrip",
		Title:     "Round trip",
		Directory: t.TempDir(),
		Mode:      "build",
		Status:    StatusFailed,
		CreatedAt: 10,
		UpdatedAt: 20,
		Tasks: []TaskState{{
			ID: "a", Prompt: "do the thing", Status: StatusFailed,
			SessionID: "ses_hoxi_1", Error: "boom",
		}},
	}
	if err := store.Save(plan); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, plan.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); runtime.GOOS != "windows" && perm != 0o600 {
		t.Fatalf("plan file permissions = %o, want 600", perm)
	}
	plans, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].ID != plan.ID || plans[0].Mode != "build" {
		t.Fatalf("loaded plans = %#v", plans)
	}
	if len(plans[0].Tasks) != 1 || plans[0].Tasks[0].Prompt != "do the thing" {
		t.Fatalf("loaded tasks = %#v", plans[0].Tasks)
	}
	if err := store.Delete(plan.ID); err != nil {
		t.Fatal(err)
	}
	plans, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 0 {
		t.Fatalf("plans after delete = %#v", plans)
	}
}

func TestStoreIgnoresCorruptFiles(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ignored.txt"), []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	plans, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 0 {
		t.Fatalf("loaded plans = %#v", plans)
	}
}

func TestStoreRejectsUnsafePlanID(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := store.Save(Plan{ID: "../escape"}); err == nil {
		t.Fatal("expected unsafe plan id to be rejected")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.json")); !os.IsNotExist(err) {
		t.Fatalf("plan escaped the store directory: %v", err)
	}
}

func TestStoreSaveSeqDropsStaleSnapshot(t *testing.T) {
	store := NewStore(t.TempDir())
	newer := Plan{
		ID:     "plan_seq_test",
		Status: StatusSucceeded,
		Tasks:  []TaskState{{ID: "a", Prompt: "work", Status: StatusSucceeded, SessionID: "ses_new"}},
	}
	stale := Plan{
		ID:     "plan_seq_test",
		Status: StatusRunning,
		Tasks:  []TaskState{{ID: "a", Prompt: "work", Status: StatusRunning, SessionID: "ses_old"}},
	}
	if err := store.SaveSeq(newer, 2); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSeq(stale, 1); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Status != StatusSucceeded || loaded[0].Tasks[0].SessionID != "ses_new" {
		t.Fatalf("stale snapshot overwrote newer state: %#v", loaded)
	}
}
