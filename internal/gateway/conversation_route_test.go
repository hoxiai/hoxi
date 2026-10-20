package gateway

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/runner"
	"github.com/hoxiai/hoxi/internal/skills"
)

type upstreamCall struct {
	method, path, query, body string
}

func newConversationGateway(t *testing.T) (*Gateway, func() []upstreamCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []upstreamCall
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, upstreamCall{r.Method, r.URL.Path, r.URL.RawQuery, string(body)})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/session":
			_, _ = io.WriteString(w, `[{"id":"ses_oc","time":{"updated":5}},{"id":"ses_engine","time":{"updated":9}},{"id":"ses_tagged","time":{"updated":7},"metadata":{"hoxiConversation":"ses_cld_owned"}}]`)
		case r.URL.Path == "/session/status":
			_, _ = io.WriteString(w, `{"ses_oc":{"type":"retry","attempt":2},"ses_engine":{"type":"busy"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			_, _ = io.WriteString(w, `{"id":"ses_new","title":"created"}`)
		default:
			_, _ = io.WriteString(w, `[]`)
		}
	}))
	t.Cleanup(upstream.Close)

	root := t.TempDir()
	t.Setenv("HOXI_SESSION_STORE_DIR", t.TempDir())
	gw := New(upstream.Listener.Addr().(*net.TCPAddr).Port, "pass", root, nil, nil)
	rec := runner.SessionRecord{ID: "ses_cld_owned", Runtime: "claude-code", ProjectDir: root, WorktreePath: root,
		SandboxMode: "local", Title: "owned", UpdatedAt: 10, OpenCodeIDs: []string{"ses_engine"}}
	if err := gw.runner.Store().Put(rec); err != nil {
		t.Fatal(err)
	}
	// Re-read the store so the engine handle is registered as managed.
	gw.runner.ConfigureOpenCode(upstream.URL, "pass")
	return gw, func() []upstreamCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]upstreamCall(nil), calls...)
	}
}

func serve(gw *Gateway, method, target, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	gw.ServeHTTP(rec, localRequest(method, target, reader))
	return rec
}

func TestConversationListMergesOwnersAndHidesEngineHandles(t *testing.T) {
	gw, _ := newConversationGateway(t)
	res := serve(gw, http.MethodGet, "/api/hoxi/sessions", "")
	var list []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil || res.Code != http.StatusOK {
		t.Fatalf("list: %d %s", res.Code, res.Body.String())
	}
	if len(list) != 2 || list[0].ID != "ses_cld_owned" || list[1].ID != "ses_oc" {
		t.Fatalf("list = %+v", list)
	}

	res = serve(gw, http.MethodGet, "/api/hoxi/sessions/status", "")
	var statuses map[string]map[string]interface{}
	if err := json.Unmarshal(res.Body.Bytes(), &statuses); err != nil {
		t.Fatalf("status: %s", res.Body.String())
	}
	if _, leaked := statuses["ses_engine"]; leaked || statuses["ses_oc"]["attempt"] != float64(2) {
		t.Fatalf("statuses = %v", statuses)
	}
}

func TestConversationRoutesToOwningBackend(t *testing.T) {
	gw, calls := newConversationGateway(t)

	res := serve(gw, http.MethodGet, "/api/hoxi/sessions/ses_cld_owned", "")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"id":"ses_cld_owned"`) {
		t.Fatalf("owned get: %d %s", res.Code, res.Body.String())
	}
	before := len(calls())

	res = serve(gw, http.MethodGet, "/api/hoxi/sessions/ses_oc/message?limit=100", "")
	got := calls()
	if res.Code != http.StatusOK || len(got) != before+1 || got[before].path != "/session/ses_oc/message" || got[before].query != "limit=100" {
		t.Fatalf("unadopted message route: %d %+v", res.Code, got)
	}

	res = serve(gw, http.MethodPost, "/api/hoxi/sessions/ses_cld_owned/summarize", "{}")
	if res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != "false" {
		t.Fatalf("owned summarize: %d %s", res.Code, res.Body.String())
	}
	res = serve(gw, http.MethodPost, "/api/hoxi/sessions/ses_cld_owned/revert", `{"messageID":"msg"}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"revert":{"messageID":"msg"}`) {
		t.Fatalf("owned revert: %d %s", res.Code, res.Body.String())
	}
	res = serve(gw, http.MethodGet, "/api/hoxi/sessions/ses_cld_owned/diff", "")
	if res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != "[]" {
		t.Fatalf("owned local session with no edits must return empty diff: %d %s", res.Code, res.Body.String())
	}
	if len(calls()) != before+1 {
		t.Fatalf("owned routes reached OpenCode: %+v", calls())
	}

	res = serve(gw, http.MethodPost, "/api/hoxi/sessions", `{"title":"created"}`)
	got = calls()
	last := got[len(got)-1]
	var created runner.SessionInfo
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v (%s)", err, res.Body.String())
	}
	if res.Code != http.StatusOK || last.method != http.MethodPost || last.path != "/session" || !strings.Contains(last.body, `"title":"created"`) || !strings.Contains(last.body, `"hoxiConversation":"`+created.ID+`"`) {
		t.Fatalf("create: %d %+v", res.Code, last)
	}
	if !strings.HasPrefix(created.ID, "ses_hoxi_") || !created.Managed || created.Runtime != "opencode" || !gw.runner.OwnsSession(created.ID) || !gw.runner.IsManagedOpenCodeSession("ses_new") {
		t.Fatalf("created session not owned by Hoxi: %+v", created)
	}
	rec, ok := gw.runner.Store().Get(created.ID)
	if !ok || rec.ExternalID != "ses_new" || len(rec.OpenCodeIDs) != 1 || rec.OpenCodeIDs[0] != "ses_new" {
		t.Fatalf("expected backing OpenCode handle ses_new in OpenCodeIDs: %+v", rec)
	}
	beforeOwnedGet := len(calls())
	if getRes := serve(gw, http.MethodGet, "/api/hoxi/sessions/"+created.ID, ""); getRes.Code != http.StatusOK || len(calls()) != beforeOwnedGet {
		t.Fatalf("newly created OpenCode session did not route to Hoxi store: %d", getRes.Code)
	}

	if res := serve(gw, http.MethodGet, "/api/hoxi/sessions/", ""); res.Code != http.StatusNotFound {
		t.Fatalf("empty id: %d", res.Code)
	}
}

func TestSkillSelectionAdoptsOldSessionAndStripsUpstreamField(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOXI_SESSION_STORE_DIR", t.TempDir())

	var mu sync.Mutex
	var postedBodies []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/skill":
			_, _ = io.WriteString(w, `[{"name":"review-changes","description":"Review diff","location":"<built-in>","content":"# Review steps"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/session/ses_legacy":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "ses_legacy",
				"directory": root,
				"title":     "Legacy Chat",
				"time":      map[string]int64{"created": 1, "updated": 2},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/session/status":
			_, _ = io.WriteString(w, `{}`)
		case r.Method == http.MethodGet && r.URL.Path == "/session/ses_legacy/message":
			_, _ = io.WriteString(w, `[]`)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done()
		case r.Method == http.MethodPost && r.URL.Path == "/session/ses_legacy/message":
			mu.Lock()
			postedBodies = append(postedBodies, string(body))
			mu.Unlock()
			_, _ = io.WriteString(w, `{}`)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	defer upstream.Close()

	gw := New(upstream.Listener.Addr().(*net.TCPAddr).Port, "pass", root, nil, nil)

	// 1. Fetch skill list via gateway
	listRes := serve(gw, http.MethodGet, "/api/hoxi/skills/list?directory="+url.QueryEscape(root), "")
	if listRes.Code != http.StatusOK {
		t.Fatalf("skills list: %d %s", listRes.Code, listRes.Body.String())
	}
	var skillList skills.ListResponse
	if err := json.Unmarshal(listRes.Body.Bytes(), &skillList); err != nil || len(skillList.Skills) != 1 {
		t.Fatalf("unexpected skill list: %+v (%v)", skillList, err)
	}
	skillKey := skillList.Skills[0].Key

	// 2. Reject invalid skill key before dispatching
	badPrompt := `{"agent":"build","parts":[{"type":"text","text":"check diff"}],"skillSelection":{"key":"skl_wrong","name":"review-changes"}}`
	badRes := serve(gw, http.MethodPost, "/api/hoxi/sessions/ses_legacy/prompt_async?directory="+url.QueryEscape(root), badPrompt)
	if badRes.Code != http.StatusConflict {
		t.Fatalf("expected 409 for mismatched skill key, got %d: %s", badRes.Code, badRes.Body.String())
	}

	// 3. Valid skillSelection adopts legacy OpenCode session and strips skillSelection from upstream payload
	goodPrompt := `{"agent":"build","parts":[{"type":"text","text":"请审查这次改动"}],"skillSelection":{"key":"` + skillKey + `","name":"review-changes"}}`
	goodRes := serve(gw, http.MethodPost, "/api/hoxi/sessions/ses_legacy/prompt_async?directory="+url.QueryEscape(root), goodPrompt)
	if goodRes.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid skillSelection prompt, got %d: %s", goodRes.Code, goodRes.Body.String())
	}
	if !gw.runner.OwnsSession("ses_legacy") {
		t.Fatalf("expected ses_legacy to be adopted into runner store")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(postedBodies)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	if len(postedBodies) != 1 {
		mu.Unlock()
		t.Fatalf("expected 1 upstream message post, got %d", len(postedBodies))
	}
	sentBody := postedBodies[0]
	mu.Unlock()

	if strings.Contains(sentBody, "skillSelection") || strings.Contains(sentBody, "hoxiSkill") {
		t.Fatalf("upstream payload must not contain skillSelection or hoxiSkill metadata: %s", sentBody)
	}
	if !strings.Contains(sentBody, "review-changes") || !strings.Contains(sentBody, "请审查这次改动") {
		t.Fatalf("upstream payload missing skill instruction or user text: %s", sentBody)
	}
}
