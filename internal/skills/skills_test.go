package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillsListAndDetailContract(t *testing.T) {
	tmpRoot := t.TempDir()
	workspaceA := filepath.Join(tmpRoot, "workspace-a")
	workspaceB := filepath.Join(tmpRoot, "workspace-b")
	skillFileA := filepath.Join(workspaceA, ".opencode", "skills", "review-changes", "SKILL.md")
	skillFileB := filepath.Join(workspaceB, ".opencode", "skills", "review-changes", "SKILL.md")

	if err := os.MkdirAll(filepath.Dir(skillFileA), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(skillFileB), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillFileA, []byte("---\nname: review-changes\ndescription: Review diff\n---\n# Body A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillFileB, []byte("---\nname: review-changes\ndescription: Review diff B\n---\n# Body B\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "opencode" || pass != "secret-pw" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		dir := r.URL.Query().Get("directory")
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(dir, "workspace-b") {
			_ = json.NewEncoder(w).Encode([]OpenCodeSkillInfo{
				{
					Name:        "customize-opencode",
					Description: "Built-in skill",
					Location:    "<built-in>",
					Content:     "# Built-in content",
				},
				{
					Name:        "review-changes",
					Description: "Workspace B review",
					Location:    skillFileB,
					Content:     "# Body B",
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode([]OpenCodeSkillInfo{
			{
				Name:        "customize-opencode",
				Description: "Built-in skill",
				Location:    "<built-in>",
				Content:     "# Built-in content",
			},
			{
				Name:        "review-changes",
				Description: "Review current git changes",
				Location:    skillFileA,
				Content:     "# Body A",
			},
		})
	}))
	defer upstream.Close()

	svc := NewService(upstream.URL, "secret-pw", func(_ context.Context, directory, sessionID, reqRuntime string) (RequestContext, error) {
		if sessionID == "ses_claude" {
			return RequestContext{
				SessionID: sessionID,
				Runtime:   "claude-code",
				Directory: workspaceA,
			}, nil
		}
		if directory == "" {
			directory = workspaceA
		}
		clean := canonicalizeDir(directory)
		if clean != canonicalizeDir(workspaceA) && clean != canonicalizeDir(workspaceB) {
			return RequestContext{}, fmt.Errorf("%w: unregistered workspace directory", ErrForbidden)
		}
		rt := "opencode"
		if reqRuntime != "" {
			rt = reqRuntime
		}
		return RequestContext{
			SessionID:   sessionID,
			Runtime:     rt,
			Directory:   clean,
			ProjectRoot: clean,
		}, nil
	})

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	// 1. List workspace A skills; verify content is NOT leaked in list payload
	req := httptest.NewRequest(http.MethodGet, "/api/hoxi/skills/list?directory="+workspaceA, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"content"`) {
		t.Fatalf("list endpoint must not serialize full skill content: %s", rec.Body.String())
	}

	var listResp ListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatal(err)
	}
	if !listResp.Supported || listResp.Runtime != "opencode" || len(listResp.Skills) != 2 {
		t.Fatalf("unexpected list response: %+v", listResp)
	}

	builtinSkill := listResp.Skills[0]
	projectSkill := listResp.Skills[1]
	if builtinSkill.Source != "builtin" || builtinSkill.CanOpenLocation {
		t.Fatalf("expected builtin skill with CanOpenLocation=false, got %+v", builtinSkill)
	}
	if projectSkill.Source != "project" || !projectSkill.CanOpenLocation || projectSkill.SourceLabel != ".opencode/skills/review-changes/SKILL.md" {
		t.Fatalf("unexpected project skill descriptor: %+v", projectSkill)
	}

	// 2. Detail endpoint returns content for matching key in workspace A
	detailReq := httptest.NewRequest(http.MethodGet, "/api/hoxi/skills/detail?directory="+workspaceA+"&key="+projectSkill.Key, nil)
	detailRec := httptest.NewRecorder()
	mux.ServeHTTP(detailRec, detailReq)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on detail, got %d: %s", detailRec.Code, detailRec.Body.String())
	}
	var detail SkillDetail
	if err := json.Unmarshal(detailRec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Content != "# Body A" || detail.Name != "review-changes" {
		t.Fatalf("unexpected detail payload: %+v", detail)
	}

	// 3. Key from workspace A must NOT resolve in workspace B (directory isolation)
	crossReq := httptest.NewRequest(http.MethodGet, "/api/hoxi/skills/detail?directory="+workspaceB+"&key="+projectSkill.Key, nil)
	crossRec := httptest.NewRecorder()
	mux.ServeHTTP(crossRec, crossReq)
	if crossRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when querying workspace A key in workspace B, got %d", crossRec.Code)
	}

	// 4. VerifySelection checks key + name + directory
	if _, err := svc.VerifySelection(context.Background(), workspaceA, "", SkillSelection{
		Key:  projectSkill.Key,
		Name: "review-changes",
	}); err != nil {
		t.Fatalf("expected valid selection in workspace A, got %v", err)
	}
	if _, err := svc.VerifySelection(context.Background(), workspaceB, "", SkillSelection{
		Key:  projectSkill.Key,
		Name: "review-changes",
	}); err == nil {
		t.Fatalf("expected VerifySelection to reject workspace A key in workspace B")
	}

	// 5. Non-opencode sessions share the same catalog and can resolve inline content
	claudeReq := httptest.NewRequest(http.MethodGet, "/api/hoxi/skills/list?directory="+workspaceA+"&sessionID=ses_claude", nil)
	claudeRec := httptest.NewRecorder()
	mux.ServeHTTP(claudeRec, claudeReq)
	if claudeRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for claude-code list, got %d", claudeRec.Code)
	}
	var claudeList ListResponse
	if err := json.Unmarshal(claudeRec.Body.Bytes(), &claudeList); err != nil {
		t.Fatal(err)
	}
	if !claudeList.Supported || claudeList.Runtime != "claude-code" || len(claudeList.Skills) != 2 {
		t.Fatalf("expected claude-code to receive the catalog, got %+v", claudeList)
	}
	claudeSkill := claudeList.Skills[1]
	if claudeSkill.Runtime != "claude-code" {
		t.Fatalf("expected descriptor runtime claude-code, got %+v", claudeSkill)
	}
	detail, err := svc.ResolveSelection(context.Background(), workspaceA, "ses_claude", SkillSelection{
		Key:  claudeSkill.Key,
		Name: "review-changes",
	})
	if err != nil {
		t.Fatalf("expected claude-code selection to resolve, got %v", err)
	}
	if detail.Content != "# Body A" {
		t.Fatalf("expected inline skill content, got %q", detail.Content)
	}
}

func TestSeedDefaultsIncludesExtraSkillsDir(t *testing.T) {
	home := t.TempDir()
	extra := t.TempDir()
	t.Setenv("HOXI_HOME", home)
	t.Setenv("HOXI_EXTRA_SKILLS_DIR", extra)

	customSkill := filepath.Join(extra, "qingpu-ops", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(customSkill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(customSkill, []byte("---\nname: qingpu-ops\n---\n# Qingpu"), 0o644); err != nil {
		t.Fatal(err)
	}

	SeedDefaults()

	seeded := filepath.Join(home, "skills", "qingpu-ops", "SKILL.md")
	data, err := os.ReadFile(seeded)
	if err != nil {
		t.Fatalf("expected extra skill to be seeded at %s: %v", seeded, err)
	}
	if !strings.Contains(string(data), "# Qingpu") {
		t.Fatalf("unexpected seeded content: %s", string(data))
	}
}
