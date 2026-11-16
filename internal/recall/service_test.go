package recall

import (
	"context"
	"errors"
	"testing"
)

type fakeSource struct {
	sessions []Session
	calls    int
	err      error
}

func (f *fakeSource) Sessions(context.Context, string) ([]Session, error) {
	f.calls++
	return f.sessions, f.err
}

func fixtureSource() *fakeSource {
	return &fakeSource{sessions: []Session{
		{
			ID: "ses_a", Title: "Fix token refresh", UpdatedAt: 20,
			Turns: []Turn{
				{Role: "user", Text: "The token refresh loop is broken", Created: 10},
				{Role: "assistant", Text: "I moved the token refresh into the auth middleware", Created: 11},
			},
		},
		{
			ID: "ses_b", Title: "Unrelated work", UpdatedAt: 10,
			Turns: []Turn{{Role: "assistant", Text: "Refactored the settings panel", Created: 5}},
		},
	}}
}

func TestSearchReturnsBestTurnPerSession(t *testing.T) {
	source := fixtureSource()
	svc := NewService(source)
	response, err := svc.Search(context.Background(), "/repo", "token refresh", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 1 {
		t.Fatalf("expected one matching session, got %#v", response.Results)
	}
	if response.Results[0].SessionID != "ses_a" || response.Results[0].Role != "assistant" {
		t.Fatalf("unexpected result: %#v", response.Results[0])
	}
	if source.calls != 1 {
		t.Fatalf("source should be loaded once, got %d", source.calls)
	}
}

func TestSearchMatchesSessionTitlesWithoutTurns(t *testing.T) {
	source := &fakeSource{sessions: []Session{{ID: "ses_c", Title: "Database migration plan", UpdatedAt: 1}}}
	svc := NewService(source)
	response, err := svc.Search(context.Background(), "/repo", "migration", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 1 || response.Results[0].SessionID != "ses_c" {
		t.Fatalf("title-only session not found: %#v", response.Results)
	}
	if response.Results[0].Snippet != "Database migration plan" {
		t.Fatalf("expected title snippet, got %q", response.Results[0].Snippet)
	}
}

func TestSearchValidatesInput(t *testing.T) {
	svc := NewService(fixtureSource())
	if _, err := svc.Search(context.Background(), "", "q", 5); err == nil {
		t.Fatal("expected empty directory to fail")
	}
	source := &fakeSource{err: errors.New("upstream unavailable")}
	if _, err := NewService(source).Search(context.Background(), "/repo", "q", 5); err == nil {
		t.Fatal("expected source errors to surface")
	}
	if _, err := NewService(nil).Search(context.Background(), "/repo", "q", 5); err == nil {
		t.Fatal("expected a missing source to fail")
	}
}

func TestEmptyQueryReturnsNoResults(t *testing.T) {
	svc := NewService(fixtureSource())
	response, err := svc.Search(context.Background(), "/repo", "  ", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 0 {
		t.Fatalf("expected no results, got %#v", response.Results)
	}
}
