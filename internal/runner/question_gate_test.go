package runner

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/agyhook"
)

func asyncInMemoryHook(t *testing.T, f *gateFixture, body []byte) chan agyhook.Decision {
	t.Helper()
	ch := make(chan agyhook.Decision, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, hookPreToolPath, bytes.NewReader(body))
		req.RemoteAddr = "127.0.0.1:54321"
		req.Header.Set("Authorization", "Bearer "+f.env[agyhook.EnvHookToken])
		req.Header.Set(agyhook.HeaderSession, f.env[agyhook.EnvSessionID])
		rec := httptest.NewRecorder()
		f.svc.handleHookPreTool(rec, req)
		var d agyhook.Decision
		if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
			t.Errorf("bad hook response %q: %v", rec.Body.String(), err)
		}
		ch <- d
	}()
	return ch
}

func TestHookGateQuestionReplyAndToolPartDecoration(t *testing.T) {
	t.Parallel()
	f := newGateFixture(t)
	events, unsub := f.svc.Subscribe("ses_agy_gate")
	defer unsub()

	f.svc.store.UpsertTurnInfo("ses_agy_gate", MessageInfo{
		ID:        "msg_a_1",
		SessionID: "ses_agy_gate",
		Role:      "assistant",
	})

	body, _ := json.Marshal(map[string]interface{}{
		"conversationId": "conv-1",
		"stepIdx":        3,
		"toolCall": map[string]interface{}{
			"name": "ask_question",
			"args": map[string]interface{}{
				"questions": []interface{}{
					map[string]interface{}{
						"question":        "Which approach should we take?",
						"options":         []interface{}{"(Recommended) Option A", "Option B"},
						"is_multi_select": true,
					},
				},
			},
		},
	})
	result := asyncInMemoryHook(t, f, body)

	asked := waitForEvent(t, events, "question.asked")
	qid, _ := asked.Properties["id"].(string)
	if !strings.HasPrefix(qid, runnerQuestionID) {
		t.Fatalf("expected runner question id prefix %q, got %q", runnerQuestionID, qid)
	}

	// Verify GET /api/hoxi/question lists the pending question with normalized options.
	listReq := httptest.NewRequest(http.MethodGet, "/api/hoxi/question?directory="+f.dir, nil)
	listRec := httptest.NewRecorder()
	f.srv.Config.Handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET /api/hoxi/question status = %d", listRec.Code)
	}
	var listed []map[string]interface{}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil || len(listed) != 1 {
		t.Fatalf("expected 1 pending question, got %s (err=%v)", listRec.Body.String(), err)
	}
	qs, _ := listed[0]["questions"].([]interface{})
	if len(qs) != 1 {
		t.Fatalf("expected 1 normalized question, got %#v", listed[0]["questions"])
	}
	qMap, _ := qs[0].(map[string]interface{})
	if qMap["multiple"] != true {
		t.Fatalf("expected multiple=true from is_multi_select, got %#v", qMap)
	}
	opts, _ := qMap["options"].([]interface{})
	if len(opts) != 2 {
		t.Fatalf("expected 2 normalized options, got %#v", qMap["options"])
	}

	// Reply via POST /api/hoxi/question/{id}/reply.
	replyBody, _ := json.Marshal(map[string]interface{}{
		"answers": [][]string{{"(Recommended) Option A"}},
	})
	replyReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/question/"+qid+"/reply", bytes.NewReader(replyBody))
	replyRec := httptest.NewRecorder()
	f.srv.Config.Handler.ServeHTTP(replyRec, replyReq)
	if replyRec.Code != http.StatusOK {
		t.Fatalf("POST /api/hoxi/question/%s/reply status = %d: %s", qid, replyRec.Code, replyRec.Body.String())
	}

	repliedEvt := waitForEvent(t, events, "question.replied")
	if repliedEvt.Properties["requestID"] != qid {
		t.Fatalf("expected question.replied for %s, got %#v", qid, repliedEvt.Properties)
	}

	dec := <-result
	if dec.Decision != hookDecisionDeny {
		t.Fatalf("expected hook decision deny to feed answers back to CLI, got %q", dec.Decision)
	}
	if !strings.Contains(dec.Reason, `"(Recommended) Option A"`) || !strings.HasPrefix(dec.Reason, questionAnsweredPrefix) {
		t.Fatalf("unexpected answer reason: %q", dec.Reason)
	}

	// Simulate Antigravity emitting a tool step ERROR after pre-tool hook deny,
	// and verify broadcast decorates it into a completed question part with answers.
	tr := NewTurnTranslator("ses_agy_gate", string(RuntimeAntigravity), "msg_a_1", "msg_user", "antigravity", "gemini-3.8-flash-low", "build", "conv-1")
	line := `{"event":"step_update","conversation_id":"conv-1","step_update":{"step_index":3,"step_type":"tool","state":"ERROR","tool_name":"ask_question","tool_info":{"name":"ask_question","error":{"message":"tool call denied by pre-tool hook: ` + strings.ReplaceAll(dec.Reason, `"`, `\"`) + `"}}}}`
	f.svc.broadcast("ses_agy_gate", tr.FeedLine(line)...)

	rec, ok := f.svc.store.Get("ses_agy_gate")
	if !ok || len(rec.Turns) == 0 {
		t.Fatalf("expected stored turn in ses_agy_gate")
	}
	var foundPart *MessagePart
	for i := range rec.Turns {
		for j := range rec.Turns[i].Parts {
			p := &rec.Turns[i].Parts[j]
			if p.Tool == "question" && p.CallID == "call_msg_a_1_3" {
				foundPart = p
			}
		}
	}
	if foundPart == nil || foundPart.State == nil {
		t.Fatalf("expected persisted question tool part")
	}
	if foundPart.State.Status != "completed" {
		t.Fatalf("expected decorated status=completed, got %q (err=%q)", foundPart.State.Status, foundPart.State.Error)
	}
	if foundPart.State.Error != "" {
		t.Fatalf("expected empty error on answered question part, got %q", foundPart.State.Error)
	}
	if !strings.HasPrefix(foundPart.State.Output, questionAnsweredPrefix) {
		t.Fatalf("expected answered output on question part, got %q", foundPart.State.Output)
	}
	if foundPart.State.Metadata == nil || foundPart.State.Metadata["answers"] == nil {
		t.Fatalf("expected metadata.answers on question part, got %#v", foundPart.State.Metadata)
	}
}

func TestHookGateQuestionReject(t *testing.T) {
	t.Parallel()
	f := newGateFixture(t)
	events, unsub := f.svc.Subscribe("ses_agy_gate")
	defer unsub()

	body, _ := json.Marshal(map[string]interface{}{
		"conversationId": "conv-1",
		"stepIdx":        4,
		"toolCall": map[string]interface{}{
			"name": "ask_question",
			"args": map[string]interface{}{
				"questions": `[{"question":"Continue?","options":["Yes","No"]}]`,
			},
		},
	})
	result := asyncInMemoryHook(t, f, body)

	asked := waitForEvent(t, events, "question.asked")
	qid, _ := asked.Properties["id"].(string)
	if qid == "" {
		t.Fatalf("missing question id")
	}

	rejectReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/question/"+qid+"/reject", bytes.NewReader([]byte(`{}`)))
	rejectRec := httptest.NewRecorder()
	f.srv.Config.Handler.ServeHTTP(rejectRec, rejectReq)
	if rejectRec.Code != http.StatusOK {
		t.Fatalf("POST /api/hoxi/question/%s/reject status = %d", qid, rejectRec.Code)
	}

	waitForEvent(t, events, "question.rejected")
	dec := <-result
	if dec.Decision != hookDecisionDeny || dec.Reason != questionDismissedReason {
		t.Fatalf("unexpected reject decision: %+v", dec)
	}
}

func TestDeepQuestionCannotBeAnsweredFromOtherWorktree(t *testing.T) {
	t.Parallel()
	f := newGateFixture(t)
	events, unsub := f.svc.Subscribe("ses_agy_gate")
	defer unsub()
	payload := []byte(`{"conversationId":"conv-1","stepIdx":5,"toolCall":{"name":"ask_question","args":{"questions":[{"question":"Continue?","options":["Yes","No"]}]}}}`)
	result := asyncInMemoryHook(t, f, payload)
	asked := waitForEvent(t, events, "question.asked")
	id, _ := asked.Properties["id"].(string)
	other := t.TempDir()
	body := []byte(`{"answers":[["Yes"]]}`)
	route := "/api/hoxi/question/" + id + "/reply?"
	req := httptest.NewRequest(http.MethodPost, route+url.Values{"directory": {other}}.Encode(), bytes.NewReader(body))
	rec := httptest.NewRecorder()
	f.srv.Config.Handler.ServeHTTP(rec, req)
	if rec.Code >= 200 && rec.Code < 300 {
		dec := <-result
		t.Fatalf("reply scoped to unrelated directory returned %d and settled pending question: %q", rec.Code, dec.Reason)
	}
	req = httptest.NewRequest(http.MethodPost, route+url.Values{"directory": {f.dir}}.Encode(), bytes.NewReader(body))
	rec2 := httptest.NewRecorder()
	f.srv.Config.Handler.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("valid reply failed: %d", rec2.Code)
	}
	<-result
}

func TestDeepRejectedPromptDoesNotChangeRuntime(t *testing.T) {
	t.Parallel()
	f := newGateFixture(t)
	prior, ok := f.svc.store.Get("ses_agy_gate")
	if !ok {
		t.Fatal("missing seeded session")
	}
	payload := []byte(`{"text":"Audit rejected prompt","runtime":"opencode","skillSelection":{"key":"invalid-key","name":"test"}}`)
	endpoint := "/api/hoxi/runner/sessions/ses_agy_gate/prompt_async?" + url.Values{"directory": {f.dir}}.Encode()
	req := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	f.srv.Config.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "invalid skillSelection") {
		t.Fatalf("expected rejected skill request: %d %s", rec.Code, rec.Body.String())
	}
	got, ok := f.svc.store.Get("ses_agy_gate")
	if !ok || got.Runtime != prior.Runtime || got.ExternalID != prior.ExternalID {
		t.Fatalf("rejected prompt changed runtime/resume ID: before=%q/%q after=%q/%q", prior.Runtime, prior.ExternalID, got.Runtime, got.ExternalID)
	}
}

func TestVerifyEngineStartupFailurePreservesPreviousRuntime(t *testing.T) {
	t.Parallel()
	f := newGateFixture(t)
	prior, ok := f.svc.store.Get("ses_agy_gate")
	if !ok {
		t.Fatal("missing seeded session")
	}
	payload := []byte(`{"text":"Audit startup failure","runtime":"opencode"}`)
	endpoint := "/api/hoxi/runner/sessions/ses_agy_gate/prompt_async?" + url.Values{"directory": {f.dir}}.Encode()
	req := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	f.srv.Config.Handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("expected startup failure when OpenCode is unavailable, got 200")
	}
	got, ok := f.svc.store.Get("ses_agy_gate")
	if !ok || got.Runtime != prior.Runtime || got.ExternalID != prior.ExternalID {
		t.Fatalf("startup failure %s changed runtime/resume ID: before=%q/%q after=%q/%q", rec.Body.String(), prior.Runtime, prior.ExternalID, got.Runtime, got.ExternalID)
	}
}

func TestReviewEventConnectFailureCleansCreatedEngineSession(t *testing.T) {
	t.Parallel()
	f := newGateFixture(t)
	deletes := 0
	ocSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			writeJSON(w, http.StatusOK, map[string]interface{}{"id": "ses_oc_orphan"})
		case r.Method == http.MethodDelete && r.URL.Path == "/session/ses_oc_orphan":
			deletes++
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			writeJSONError(w, http.StatusServiceUnavailable, "event stream unavailable")
		default:
			writeJSON(w, http.StatusOK, map[string]interface{}{})
		}
	}))
	t.Cleanup(ocSrv.Close)
	f.svc.ConfigureOpenCode(ocSrv.URL, "")

	payload := []byte(`{"text":"Audit event connect failure","runtime":"opencode"}`)
	endpoint := "/api/hoxi/runner/sessions/ses_agy_gate/prompt_async?" + url.Values{"directory": {f.dir}}.Encode()
	req := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	f.srv.Config.Handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("expected startup failure when /event fails, got 200")
	}
	got, _ := f.svc.store.Get("ses_agy_gate")
	_, managed := f.svc.managedOpenCode.Load("ses_oc_orphan")
	if managed || deletes != 1 || len(got.OpenCodeIDs) != 0 {
		t.Fatalf("startup rollback left created engine session: managed=%v deletes=%d restored OpenCodeIDs=%v", managed, deletes, got.OpenCodeIDs)
	}
}
