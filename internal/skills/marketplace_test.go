package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const marketplaceBody = "---\nname: demo-skill\ndescription: Demo skill\n---\n# Demo\n"

func digestOf(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func newMarketplaceServer(t *testing.T, body string, digest string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/hoxi/skills":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"data": map[string]any{
					"items": []map[string]any{{
						"slug":          "demo-skill",
						"name":          "demo-skill",
						"description":   "Demo skill",
						"version":       "1.0.0",
						"runtime":       "opencode",
						"contentUrl":    "http://" + r.Host + "/skills/demo-skill/SKILL.md",
						"contentSha256": digest,
					}},
				},
			})
		case "/skills/demo-skill/SKILL.md":
			_, _ = w.Write([]byte(body))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestMarketplaceListsAndInstallsVerifiedSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	upstream := newMarketplaceServer(t, marketplaceBody, digestOf(marketplaceBody))
	defer upstream.Close()

	svc := NewMarketplaceService(upstream.URL, upstream.Client())
	list, err := svc.List(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Skills) != 1 || list.Skills[0].Installed {
		t.Fatalf("unexpected catalog: %+v", list.Skills)
	}

	installed, err := svc.Install(context.Background(), "demo-skill", false)
	if err != nil {
		t.Fatal(err)
	}
	if !installed.Installed {
		t.Fatal("expected installed entry")
	}
	target := filepath.Join(home, ".hoxi", "skills", "demo-skill", "SKILL.md")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != marketplaceBody {
		t.Fatalf("installed body = %q", string(data))
	}

	// A second install of identical content is a no-op, not an error, and still reports Installed=true.
	again, err := svc.Install(context.Background(), "demo-skill", false)
	if err != nil {
		t.Fatalf("idempotent install failed: %v", err)
	}
	if !again.Installed || again.InstalledDigest == "" {
		t.Fatalf("idempotent install should report Installed=true, got %+v", again)
	}
}

func TestMarketplaceRejectsDigestMismatchWithoutWriting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	upstream := newMarketplaceServer(t, marketplaceBody, strings.Repeat("0", 64))
	defer upstream.Close()

	svc := NewMarketplaceService(upstream.URL, upstream.Client())
	_, err := svc.Install(context.Background(), "demo-skill", false)
	if !errors.Is(err, ErrSkillDigestMismatch) {
		t.Fatalf("err = %v, want digest mismatch", err)
	}
	target := filepath.Join(home, ".hoxi", "skills", "demo-skill", "SKILL.md")
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatal("digest mismatch must not write the skill file")
	}
}

func TestMarketplaceRefusesOverwriteOfUserEditedSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	upstream := newMarketplaceServer(t, marketplaceBody, digestOf(marketplaceBody))
	defer upstream.Close()

	target := filepath.Join(home, ".hoxi", "skills", "demo-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("---\nname: demo-skill\ndescription: local edit\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewMarketplaceService(upstream.URL, upstream.Client())
	if _, err := svc.Install(context.Background(), "demo-skill", false); !errors.Is(err, ErrSkillAlreadyInstalled) {
		t.Fatalf("err = %v, want already installed", err)
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "local edit") {
		t.Fatal("local edits must survive a refused overwrite")
	}

	if _, err := svc.Install(context.Background(), "demo-skill", true); err != nil {
		t.Fatalf("explicit overwrite failed: %v", err)
	}
	after, err = os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != marketplaceBody {
		t.Fatal("explicit overwrite should replace the file")
	}
}

func TestMarketplaceRejectsInvalidSlugsAndHosts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	svc := NewMarketplaceService("https://hoxi.ai", nil)

	for _, slug := range []string{"", "../escape", "Upper", "with space", "-leading", strings.Repeat("a", 65)} {
		if _, err := svc.Install(context.Background(), slug, false); !errors.Is(err, ErrInvalidSkillSlug) {
			t.Fatalf("slug %q err = %v, want invalid slug", slug, err)
		}
	}

	for _, rawURL := range []string{
		"https://evil.example.com/skill.md",
		"http://evil.example.com/skill.md",
		"file:///etc/passwd",
		"https://hoxi.ai.evil.com/skill.md",
	} {
		if _, err := sanitizeMarketplaceSkill(MarketplaceSkill{
			Slug:          "demo-skill",
			ContentURL:    rawURL,
			ContentSHA256: strings.Repeat("a", 64),
		}); err == nil {
			t.Fatalf("contentUrl %q should be rejected", rawURL)
		}
	}

	cleaned, err := sanitizeMarketplaceSkill(MarketplaceSkill{
		Slug:          "demo-skill",
		ContentURL:    "https://hoxi.ai/skills/demo-skill/SKILL.md",
		ContentSHA256: strings.Repeat("a", 64),
		RepoURL:       "javascript:alert(1)",
		HomepageURL:   "https://hoxi.ai/skills/demo-skill",
	})
	if err != nil {
		t.Fatalf("sanitizeMarketplaceSkill failed: %v", err)
	}
	if cleaned.RepoURL != "" {
		t.Fatalf("javascript: repoUrl must be stripped, got %q", cleaned.RepoURL)
	}
	if cleaned.HomepageURL != "https://hoxi.ai/skills/demo-skill" {
		t.Fatalf("https homepageUrl should be preserved, got %q", cleaned.HomepageURL)
	}
}

func TestMarketplaceRejectsRedirectToUnapprovedHost(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	redirectSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/hoxi/skills":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"data": map[string]any{
					"items": []map[string]any{{
						"slug":          "demo-skill",
						"name":          "demo-skill",
						"contentUrl":    "http://" + r.Host + "/redirect",
						"contentSha256": digestOf(marketplaceBody),
					}},
				},
			})
		case "/redirect":
			http.Redirect(w, r, "https://evil.example.com/SKILL.md", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer redirectSrv.Close()

	svc := NewMarketplaceService(redirectSrv.URL, redirectSrv.Client())
	if _, err := svc.Install(context.Background(), "demo-skill", false); err == nil {
		t.Fatal("redirect to unapproved host must be rejected")
	}
}

func TestMarketplaceInstallRouteRejectsBadBodyAndMethod(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	upstream := newMarketplaceServer(t, marketplaceBody, digestOf(marketplaceBody))
	defer upstream.Close()

	svc := NewService("", "", nil)
	svc.SetMarketplace(NewMarketplaceService(upstream.URL, upstream.Client()))
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	badReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/skills/marketplace/install", strings.NewReader("{not json"))
	badRec := httptest.NewRecorder()
	mux.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON status = %d, want 400", badRec.Code)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/hoxi/skills/marketplace/install", nil)
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET install status = %d, want 405", getRec.Code)
	}
}
