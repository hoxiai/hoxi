package gateway

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hoxiai/hoxi/internal/runner"
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
	if res.Code != http.StatusNotImplemented {
		t.Fatalf("owned revert: %d", res.Code)
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

// Guards must judge the rewritten path, or the facade would bypass AI consent.
func TestConversationPromptsStillRequireConsent(t *testing.T) {
	gw, calls := newConversationGateway(t)
	for _, id := range []string{"ses_cld_owned", "ses_oc"} {
		res := serve(gw, http.MethodPost, "/api/hoxi/sessions/"+id+"/prompt_async", `{"parts":[{"type":"text","text":"hi"}]}`)
		if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "privacy_consent_required") {
			t.Fatalf("%s prompt without consent: %d %s", id, res.Code, res.Body.String())
		}
	}
	for _, c := range calls() {
		if strings.HasSuffix(c.path, "/prompt_async") {
			t.Fatalf("prompt reached OpenCode without consent: %+v", c)
		}
	}
}
