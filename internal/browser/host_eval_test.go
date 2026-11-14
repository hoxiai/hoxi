package browser

import (
	"context"
	"testing"
)

func TestResolveHostScopeIsolatesSecondaryStoresFromDefaultScope(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetDefaultWindowMode(true)
	s.SetProfileResolver(func(sessionID, _ string) string {
		switch sessionID {
		case "workstore_store-a", "workstore_default":
			return "default"
		case "workstore_store-b":
			return "store-b"
		default:
			return ""
		}
	})

	dir := t.TempDir()
	liveCtx, liveCancel := context.WithCancel(context.Background())
	defer liveCancel()

	defaultScope := &scope{
		service:   s,
		sessionID: "workstore_default",
		directory: dir,
		window:    true,
		browser:   liveCtx,
		gate:      make(chan struct{}, 1),
		ctx:       liveCtx,
		cancel:    func() {},
	}
	s.mu.Lock()
	s.scopes[scopeKey{directory: dir, session: "workstore_default"}] = defaultScope
	s.mu.Unlock()

	if got := s.resolveHostScope("workstore_store-a", dir, "store-a"); got != defaultScope {
		t.Fatalf("expected primary store-a (profile 'default') to resolve workstore_default, got %v", got)
	}
	if got := s.resolveHostScope("", dir, "store-a"); got != defaultScope {
		t.Fatalf("expected empty session with store-a to resolve workstore_default, got %v", got)
	}
	if got := s.resolveHostScope("workstore_store-b", dir, "store-b"); got != nil {
		t.Fatalf("expected secondary store-b NOT to fall back to store-a's workstore_default scope, got %v", got)
	}
	if got := s.resolveHostScope("", dir, "store-b"); got != nil {
		t.Fatalf("expected empty session with secondary store-b NOT to fall back to workstore_default, got %v", got)
	}

	// Explicitly store-bound scope must never be returned for a different storeID.
	defaultScope.env.StoreID = "store-a"
	if got := s.resolveHostScope("workstore_default", dir, "store-b"); got != nil {
		t.Fatalf("expected store-a-bound scope to be rejected for store-b, got %v", got)
	}

	//Clean up synthetic scope before s.Close() runs.
	s.mu.Lock()
	delete(s.scopes, scopeKey{directory: dir, session: "workstore_default"})
	s.mu.Unlock()
}
