package browser

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// A JavaScript dialog can hold an input command open. Observation and dialog
// answers therefore use the tracker mutex, independently of the page queue.
func (s *Service) serveWorkflows(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	agent, err := s.authenticate(r)
	if err != nil || agent {
		fail(w, 403, errors.New("only UI may inspect browser workflows"))
		return
	}
	if r.Method != "GET" && r.Method != "POST" {
		fail(w, 405, errors.New("GET or POST required"))
		return
	}
	key, err := s.requestScope(r)
	if err != nil {
		fail(w, 400, err)
		return
	}
	tab := r.URL.Query().Get("tabID")
	if len(tab) > 128 {
		fail(w, 400, errors.New("invalid tab"))
		return
	}
	s.mu.Lock()
	sc := s.scopes[key]
	s.mu.Unlock()
	if sc == nil || sc.workflows == nil {
		if r.Method == "GET" {
			writeJSON(w, 200, WorkflowState{Downloads: []Download{}})
		} else {
			fail(w, 409, errors.New("page dialog expired"))
		}
		return
	}
	if r.Method == "GET" {
		writeJSON(w, 200, sc.workflows.snapshot(tab))
		return
	}
	var answer struct {
		ID     string `json:"id"`
		Accept *bool  `json:"accept"`
		Text   string `json:"text"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&answer) != nil || answer.Accept == nil || len(answer.Text) > 16384 {
		fail(w, 400, errors.New("identified dialog answer required"))
		return
	}
	sc.workflows.mu.Lock()
	dialog, exists := sc.workflows.dialogs[tab]
	target := sc.workflows.tabs[tab]
	sc.workflows.mu.Unlock()
	if !exists || dialog.ID != answer.ID || target == nil {
		fail(w, 409, errors.New("page dialog expired"))
		return
	}
	ctx, cancel := context.WithTimeout(target, 10*time.Second)
	defer cancel()
	stop := context.AfterFunc(r.Context(), cancel)
	defer stop()
	if err := chromedp.Run(ctx, page.HandleJavaScriptDialog(*answer.Accept).WithPromptText(answer.Text)); err != nil {
		fail(w, 409, errors.New("cannot answer page dialog"))
		return
	}
	writeJSON(w, 200, sc.workflows.snapshot(tab))
}
