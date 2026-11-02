package browser

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvedScopeSharesManualOwnershipAcrossUIAndAgent(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	dir := t.TempDir()
	s.SetSessionResolver(func(id, directory string) (string, error) {
		if id == "ses_engine" {
			return "ses_hoxi", nil
		}
		if id == "ses_wrong_directory" {
			return "", errors.New("directory mismatch")
		}
		if id == "ses_invalid_resolved" {
			return "../invalid", nil
		}
		return id, nil
	})
	if w := request(t, s, dir, "ses_hoxi", "action", `{"action":"control","manual":true}`, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, endpoint := range []string{"state", "frame", "action"} {
		w := request(t, s, dir, "ses_engine", endpoint, `{"action":"screenshot"}`, true)
		if w.Code != 403 || !strings.Contains(w.Body.String(), "manual control is active") {
			t.Fatalf("agent bypassed UI control on %s: %d %s", endpoint, w.Code, w.Body.String())
		}
	}
	if w := request(t, s, dir, "ses_other", "state", "", true); w.Code != 200 || strings.Contains(w.Body.String(), `"manual":true`) {
		t.Fatal("unrelated session shared ownership", w.Code, w.Body.String())
	}
	if len(s.scopes) != 1 {
		t.Fatal("engine handle allocated another browser")
	}
	for _, id := range []string{"ses_wrong_directory", "ses_invalid_resolved"} {
		for _, endpoint := range []string{"state", "frame", "stream", "action"} {
			if w := request(t, s, dir, id, endpoint, `{"action":"snapshot"}`, false); w.Code != 400 {
				t.Fatalf("invalid scope accepted on %s: %d %s", endpoint, w.Code, w.Body.String())
			}
		}
	}
	if w := request(t, s, dir, "ses_engine", "action", `{"action":"control","manual":false}`, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(t, s, dir, "ses_engine", "state", "", true); w.Code != 200 {
		t.Fatal("returning control did not unblock engine", w.Code, w.Body.String())
	}
}

func TestProfileResolverIsolatesPersistentStoreProfiles(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	root := filepath.Join(t.TempDir(), "browser-profile")
	s.SetProfileRoot(root)
	s.SetProfileResolver(func(sessionID, _ string) string {
		switch sessionID {
		case "ses_store_a", "workstore_store-1688-a":
			return "store-1688-a"
		case "ses_store_b", "workstore_store-1688-b":
			return "store-1688-b"
		case "ses_unsafe":
			return "../escape"
		default:
			return ""
		}
	})

	dir := t.TempDir()
	// Opening/controlling via workstore_store-1688-a and then accessing via ses_store_a
	// must hit the exact same live store browser scope without allocating a duplicate scope.
	if w := request(t, s, dir, "workstore_store-1688-a", "action", `{"action":"control","manual":true}`, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(s.scopes) != 1 {
		t.Fatalf("expected 1 scope after workstore_ action, got %d", len(s.scopes))
	}
	if w := request(t, s, dir, "ses_store_a", "state", "", false); w.Code != 200 || !strings.Contains(w.Body.String(), `"manual":true`) {
		t.Fatalf("expected ses_store_a to share workstore_store-1688-a scope state, got %d %s", w.Code, w.Body.String())
	}
	if len(s.scopes) != 1 {
		t.Fatalf("expected ses_store_a to reuse the store scope, got %d scopes", len(s.scopes))
	}

	scA := &scope{service: s, sessionID: "workstore_store-1688-a", directory: dir, window: true}
	profA, err := s.acquirePersistentProfile(scA)
	if err != nil {
		t.Fatalf("acquirePersistentProfile for store A failed: %v", err)
	}
	wantA := filepath.Join(root+"-stores", "store-1688-a")
	if profA != wantA {
		t.Fatalf("expected store A profile %q, got %q", wantA, profA)
	}

	scB := &scope{service: s, sessionID: "ses_store_b", directory: dir, window: true}
	profB, err := s.acquirePersistentProfile(scB)
	if err != nil {
		t.Fatalf("acquirePersistentProfile for store B failed: %v", err)
	}
	wantB := filepath.Join(root+"-stores", "store-1688-b")
	if profB != wantB {
		t.Fatalf("expected store B profile %q, got %q", wantB, profB)
	}

	scUnsafe := &scope{service: s, sessionID: "ses_unsafe", directory: dir, window: true}
	profUnsafe, err := s.acquirePersistentProfile(scUnsafe)
	if err != nil {
		t.Fatalf("acquirePersistentProfile for unsafe failed: %v", err)
	}
	if profUnsafe != root {
		t.Fatalf("expected unsafe profile key to fall back to root %q, got %q", root, profUnsafe)
	}
}
