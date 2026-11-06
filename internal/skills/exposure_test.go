package skills

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type skillFixture struct {
	home, project, hoxiDir string
	files                  map[string]string // skill name -> SKILL.md path
}

func writeSkill(t *testing.T, path, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "---\nname: " + name + "\ndescription: " + name + " description\n---\n" + body
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newSkillFixture lays out skills the way a real machine has them: Hoxi's
// managed directory, a cross-client ~/.agents skill, a Claude app account sync
// bucket with a manifest, a skill the user put in ~/.claude/skills directly,
// and a project skill.
func newSkillFixture(t *testing.T) skillFixture {
	t.Helper()
	root := t.TempDir()
	f := skillFixture{home: filepath.Join(root, "home"), project: filepath.Join(root, "project"), files: map[string]string{}}
	f.hoxiDir = filepath.Join(f.home, ".hoxi", "skills")
	t.Setenv("HOME", f.home)
	t.Setenv("HOXI_HOME", filepath.Join(f.home, ".hoxi"))
	bucket := filepath.Join(f.home, ".claude", "skills", "synced", "bucket-1")
	add := func(name, path, body string) {
		writeSkill(t, path, name, body)
		f.files[name] = path
	}
	add("us-options-ops", filepath.Join(f.hoxiDir, "us-options-ops", "SKILL.md"), "Use hoxi_tools for option chains.")
	add("skill-authoring", filepath.Join(f.hoxiDir, "skill-authoring", "SKILL.md"), "Write SKILL.md files.")
	add("release-check", filepath.Join(f.home, ".agents", "skills", "release-check", "SKILL.md"), "Run ./verify.sh.")
	add("pdf", filepath.Join(bucket, "pdf", "SKILL.md"), "Use python scripts/fill.py.")
	add("built-in-browser", filepath.Join(bucket, "built-in-browser", "SKILL.md"), "Call `mcp__Claude_Browser__navigate` and mcp__remote-devices__Claude_Browser__read_page.")
	add("import-memory", filepath.Join(bucket, "import-memory", "SKILL.md"), "Store entries with mcp__memory__create_entities.")
	add("my-notes", filepath.Join(f.home, ".claude", "skills", "my-notes", "SKILL.md"), "Plain notes workflow.")
	add("review-changes", filepath.Join(f.project, ".agents", "skills", "review-changes", "SKILL.md"), "Review git diff.")
	manifest := `{"skills":[{"skillId":"pdf","name":"pdf"},{"skillId":"built-in-browser","name":"built-in-browser"},{"skillId":"import-memory","name":"import-memory"}]}`
	if err := os.WriteFile(filepath.Join(bucket, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

// upstreamFor serves /skill from the fixture files and /config from cfg, the
// two engine routes the catalog reads.
func upstreamFor(t *testing.T, f skillFixture, config string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/config":
			_, _ = w.Write([]byte(config))
		case "/skill":
			items := []OpenCodeSkillInfo{{Name: "customize-opencode", Description: "Built-in", Location: "<built-in>", Content: "# built-in"}}
			for name, path := range f.files {
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Error(err)
				}
				items = append(items, OpenCodeSkillInfo{Name: name, Description: name + " description", Location: path, Content: string(raw)})
			}
			_ = json.NewEncoder(w).Encode(items)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fixtureService(srv *httptest.Server, f skillFixture, runtime string) *Service {
	return NewService(srv.URL, "pw", func(_ context.Context, directory, sessionID, _ string) (RequestContext, error) {
		return RequestContext{SessionID: sessionID, Runtime: runtime, Directory: f.project, ProjectRoot: f.project}, nil
	})
}

func listExposures(t *testing.T, svc *Service, dir string) map[string]SkillDescriptor {
	t.Helper()
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/hoxi/skills/list?directory="+dir, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d: %s", rec.Code, rec.Body.String())
	}
	var resp ListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	out := make(map[string]SkillDescriptor, len(resp.Skills))
	for _, d := range resp.Skills {
		out[d.Name] = d
	}
	return out
}

func TestSkillsListClassifiesExposureByDependencySourceAndConfig(t *testing.T) {
	f := newSkillFixture(t)
	srv := upstreamFor(t, f, `{"mcp":{"memory":{"type":"local"},"off":{"enabled":false}}}`)
	got := listExposures(t, fixtureService(srv, f, "opencode"), f.project)

	want := map[string]struct{ exposure, reason string }{
		"customize-opencode": {ExposureAuto, ""},
		"review-changes":     {ExposureAuto, ""},                     // project skill
		"release-check":      {ExposureAuto, ""},                     // cross-client ~/.agents skill
		"my-notes":           {ExposureAuto, ""},                     // user skill directly under ~/.claude/skills
		"import-memory":      {ExposureOnDemand, reasonClientSynced}, // mcp__memory__ is configured
		"skill-authoring":    {ExposureAuto, ""},                     // general bundled Hoxi skill
		"us-options-ops":     {ExposureOnDemand, reasonHoxiManaged},
		"pdf":                {ExposureOnDemand, reasonClientSynced},
		"built-in-browser":   {ExposureUnavailable, reasonMissingTools},
	}
	if len(got) != len(want) {
		t.Fatalf("catalog has %d skills, want %d: %+v", len(got), len(want), got)
	}
	for name, w := range want {
		d := got[name]
		if d.Exposure != w.exposure || d.ExposureReason != w.reason {
			t.Errorf("%s: exposure=%q reason=%q, want %q %q", name, d.Exposure, d.ExposureReason, w.exposure, w.reason)
		}
	}
	if tools := strings.Join(got["built-in-browser"].MissingTools, ","); tools != "mcp__Claude_Browser__*,mcp__remote-devices__*" {
		t.Errorf("missing tools = %q", tools)
	}
}

func TestHoxiWorkflowSkillsStayAutoInWorkWorkspaces(t *testing.T) {
	f := newSkillFixture(t)
	srv := upstreamFor(t, f, `{}`)
	svc := NewService(srv.URL, "pw", func(_ context.Context, _, sessionID, _ string) (RequestContext, error) {
		return RequestContext{SessionID: sessionID, Runtime: "opencode", Directory: f.project, ProjectRoot: f.project, Surface: "work"}, nil
	})
	got := listExposures(t, svc, f.project)
	if d := got["us-options-ops"]; d.Exposure != ExposureAuto {
		t.Errorf("Work workspace: us-options-ops = %+v, want auto", d)
	}
	// Surface only widens Hoxi's own workflow skills; other rules still apply.
	if d := got["pdf"]; d.Exposure != ExposureOnDemand {
		t.Errorf("Work workspace: pdf = %+v, want on-demand", d)
	}
	if d := got["built-in-browser"]; d.Exposure != ExposureUnavailable {
		t.Errorf("Work workspace: built-in-browser = %+v, want unavailable", d)
	}
}

func TestSkillExposureFollowsPermissionSkillRules(t *testing.T) {
	f := newSkillFixture(t)
	// Last match wins, as in the engine: "*" deny then an exact allow keeps one
	// skill; an exact allow also keeps an on-demand skill in the catalog.
	srv := upstreamFor(t, f, `{"permission":{"bash":"ask","skill":{"*":"allow","release-*":"deny","us-options-ops":"allow","pdf":"ask"}}}`)
	got := listExposures(t, fixtureService(srv, f, "opencode"), f.project)
	if d := got["release-check"]; d.Exposure != ExposureUnavailable || d.ExposureReason != reasonConfigDenied {
		t.Errorf("release-check = %+v, want unavailable/config-denied", d)
	}
	for _, name := range []string{"us-options-ops", "pdf"} {
		if d := got[name]; d.Exposure != ExposureAuto || d.ExposureReason != reasonConfigPinned {
			t.Errorf("%s = %+v, want pinned auto", name, d)
		}
	}
	if d := got["amazon-ops"]; d.Name != "" {
		t.Errorf("unexpected skill %+v", d)
	}
}

func TestResolveSelectionRefusesUnavailableAndKeepsOnDemandSelectable(t *testing.T) {
	f := newSkillFixture(t)
	srv := upstreamFor(t, f, `{}`)
	for _, runtime := range []string{"opencode", "claude-code"} {
		svc := fixtureService(srv, f, runtime)
		got := listExposures(t, svc, f.project)

		browser := got["built-in-browser"]
		_, err := svc.ResolveSelection(context.Background(), f.project, "", SkillSelection{Key: browser.Key, Name: browser.Name})
		if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "mcp__Claude_Browser__*") {
			t.Fatalf("%s: selecting a skill with missing tools = %v, want ErrUnsupported naming the tools", runtime, err)
		}

		options := got["us-options-ops"]
		detail, err := svc.ResolveSelection(context.Background(), f.project, "", SkillSelection{Key: options.Key, Name: options.Name})
		if err != nil || !strings.Contains(detail.Content, "hoxi_tools") {
			t.Fatalf("%s: on-demand skill must stay selectable, got %v %+v", runtime, err, detail)
		}
	}
}

func TestSkillExposureNeverChangesSkillFiles(t *testing.T) {
	f := newSkillFixture(t)
	before := map[string][]byte{}
	for name, path := range f.files {
		raw, _ := os.ReadFile(path)
		before[name] = raw
	}
	srv := upstreamFor(t, f, `{"permission":{"skill":{"*":"deny"}}}`)
	_ = listExposures(t, fixtureService(srv, f, "opencode"), f.project)
	for name, path := range f.files {
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(after, before[name]) {
			t.Fatalf("%s changed on disk (err=%v)", name, err)
		}
	}
}

func TestParseSkillRulesKeepsDocumentOrder(t *testing.T) {
	cfg, err := parseEngineSkillConfig([]byte(`{"permission":{"*":"ask","skill":{"b":"deny","*":"allow","a":"deny"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range cfg.rules {
		got = append(got, r.pattern+"="+r.action)
	}
	if strings.Join(got, ",") != "*=ask,b=deny,*=allow,a=deny" {
		t.Fatalf("rules = %v", got)
	}
	if action, _ := cfg.ruleFor("b"); action != "allow" {
		t.Fatalf("b resolves to %q; a later wildcard allow must win", action)
	}
	if whole, err := parseEngineSkillConfig([]byte(`{"permission":"deny"}`)); err != nil || len(whole.rules) != 1 {
		t.Fatalf("string permission = %+v %v", whole, err)
	}
	if _, err := parseEngineSkillConfig([]byte(`[]`)); err == nil {
		t.Fatal("non-object config must be rejected")
	}
}
