package browser

import (
	"errors"
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
