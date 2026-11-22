package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hoxiai/hoxi/internal/provider"
)

func TestMediaRoutesRejectRemoteTunnelAndProtectedPaths(t *testing.T) {
	wsDir := t.TempDir()
	t.Setenv("HOME", wsDir)
	t.Setenv("HOXI_HOME", filepath.Join(wsDir, ".hoxi-home"))

	g := &Gateway{
		rootDir:   wsDir,
		mux:       http.NewServeMux(),
		providers: provider.NewService(0, "", wsDir, nil),
	}
	g.registerMediaRoutes()

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/hoxi/media/capabilities"},
		{http.MethodGet, "/api/hoxi/media/roles"},
		{http.MethodPut, "/api/hoxi/media/roles"},
		{http.MethodPost, "/api/hoxi/media/image"},
		{http.MethodPost, "/api/hoxi/media/video"},
		{http.MethodPost, "/api/hoxi/media/speech"},
		{http.MethodPost, "/api/hoxi/media/transcribe"},
	} {
		// Blocked at tunnel allow-list layer
		recTunnel := httptest.NewRecorder()
		g.serveRemoteRequest(recTunnel, httptest.NewRequest(tc.method, tc.path, bytes.NewReader([]byte(`{}`))), remoteSourceRelay)
		if recTunnel.Code != http.StatusForbidden {
			t.Fatalf("expected tunnel allow-list HTTP 403 for %s, got %d", tc.path, recTunnel.Code)
		}

		// Defense-in-depth inside handler via isRemoteRequest(r)
		req := httptest.NewRequest(tc.method, tc.path, bytes.NewReader([]byte(`{}`)))
		req = req.WithContext(context.WithValue(req.Context(), remoteSourceKey{}, remoteSourceRelay))
		recHandler := httptest.NewRecorder()
		g.mux.ServeHTTP(recHandler, req)
		if recHandler.Code != http.StatusForbidden {
			t.Fatalf("expected handler HTTP 403 for remote context on %s, got %d", tc.path, recHandler.Code)
		}
	}

	capRec := httptest.NewRecorder()
	g.mux.ServeHTTP(capRec, httptest.NewRequest(http.MethodGet, "/api/hoxi/media/capabilities", nil))
	if capRec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 on local capabilities, got %d", capRec.Code)
	}
	var capBody map[string]any
	_ = json.Unmarshal(capRec.Body.Bytes(), &capBody)
	if capBody["ok"] != true {
		t.Fatalf("expected ok=true in capabilities response, got %+v", capBody)
	}

	for _, badOut := range []string{"../escape.png", ".env", ".git/hooks/pre-commit", ".opencode/plugin.js", ".hoxi/models.json"} {
		for _, ep := range []string{"/api/hoxi/media/image", "/api/hoxi/media/video", "/api/hoxi/media/speech"} {
			raw, _ := json.Marshal(map[string]any{
				"prompt":     "icon",
				"text":       "hello",
				"directory":  wsDir,
				"outputPath": badOut,
			})
			rec := httptest.NewRecorder()
			g.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, ep, bytes.NewReader(raw)))
			if rec.Code != http.StatusForbidden {
				t.Fatalf("expected HTTP 403 on %s for unsafe outputPath %q, got %d (%s)", ep, badOut, rec.Code, rec.Body.String())
			}
			if ep != "/api/hoxi/media/speech" {
				inRaw, _ := json.Marshal(map[string]any{
					"prompt":     "animate",
					"directory":  wsDir,
					"inputPaths": []string{badOut},
				})
				inRec := httptest.NewRecorder()
				g.mux.ServeHTTP(inRec, httptest.NewRequest(http.MethodPost, ep, bytes.NewReader(inRaw)))
				if inRec.Code != http.StatusForbidden {
					t.Fatalf("expected HTTP 403 on %s for unsafe inputPaths %q, got %d (%s)", ep, badOut, inRec.Code, inRec.Body.String())
				}
			}
		}
	}

	// Symlink escape refusal: outside-workspace file, intra-workspace .env, directory symlink, and dangling symlink
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	_ = os.WriteFile(outsideFile, []byte("outside-secret"), 0o600)
	_ = os.WriteFile(filepath.Join(wsDir, ".env"), []byte("API_KEY=secret"), 0o600)

	_ = os.Symlink(outsideFile, filepath.Join(wsDir, "link_outside.png"))
	_ = os.Symlink(filepath.Join(wsDir, ".env"), filepath.Join(wsDir, "link_env.png"))
	_ = os.Symlink(outsideDir, filepath.Join(wsDir, "out_dir_link"))
	_ = os.Symlink(filepath.Join(outsideDir, "nonexistent.png"), filepath.Join(wsDir, "dangling.png"))

	for _, symPath := range []string{"link_outside.png", "link_env.png", "out_dir_link/nested/out.png", "dangling.png"} {
		for _, ep := range []string{"/api/hoxi/media/image", "/api/hoxi/media/video", "/api/hoxi/media/speech"} {
			rawOut, _ := json.Marshal(map[string]any{
				"prompt":     "icon",
				"text":       "hello",
				"directory":  wsDir,
				"outputPath": symPath,
			})
			outRec := httptest.NewRecorder()
			g.mux.ServeHTTP(outRec, httptest.NewRequest(http.MethodPost, ep, bytes.NewReader(rawOut)))
			if outRec.Code != http.StatusForbidden {
				t.Fatalf("expected HTTP 403 on %s for symlink outputPath %q, got %d (%s)", ep, symPath, outRec.Code, outRec.Body.String())
			}
		}
		for _, ep := range []string{"/api/hoxi/media/image", "/api/hoxi/media/video"} {
			rawIn, _ := json.Marshal(map[string]any{
				"prompt":     "animate",
				"directory":  wsDir,
				"inputPaths": []string{symPath},
			})
			inRec := httptest.NewRecorder()
			g.mux.ServeHTTP(inRec, httptest.NewRequest(http.MethodPost, ep, bytes.NewReader(rawIn)))
			if inRec.Code != http.StatusForbidden {
				t.Fatalf("expected HTTP 403 on %s for symlink inputPaths %q, got %d (%s)", ep, symPath, inRec.Code, inRec.Body.String())
			}
		}
	}

	// Output directory collision returns explicit error instead of silent success
	existingDirTarget := filepath.Join(wsDir, "existing_dir.mp3")
	_ = os.MkdirAll(existingDirTarget, 0o755)
	if _, dirWriteErr := writeMediaOutputFile(wsDir, existingDirTarget, []byte("audio")); dirWriteErr == nil {
		t.Fatalf("expected writeMediaOutputFile to fail when outputPath is an existing directory")
	}

	// Unconfigured local image/video/speech returns ok=false with missingCapability + fallbackTool="hoxi_tools"
	for _, tc := range []struct {
		ep   string
		want string
	}{
		{"/api/hoxi/media/image", "image"},
		{"/api/hoxi/media/video", "video"},
		{"/api/hoxi/media/speech", "speech"},
	} {
		raw, _ := json.Marshal(map[string]any{"prompt": "test", "text": "test", "directory": wsDir})
		rec := httptest.NewRecorder()
		g.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, tc.ep, bytes.NewReader(raw)))
		var res map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["ok"] != false || res["fallbackTool"] != "hoxi_tools" || res["missingCapability"] != tc.want {
			t.Fatalf("expected ok=false, missingCapability=%s, fallbackTool=hoxi_tools on %s, got %+v", tc.want, tc.ep, res)
		}
	}

	// Media role preferences round-trip via PUT / GET /api/hoxi/media/roles
	putRoleRec := httptest.NewRecorder()
	g.mux.ServeHTTP(putRoleRec, httptest.NewRequest(http.MethodPut, "/api/hoxi/media/roles", bytes.NewReader([]byte(`{"imageModel":"google/gemini-3-pro-image","videoModel":"google/veo-3.0-generate-001"}`))))
	if putRoleRec.Code != http.StatusOK {
		t.Fatalf("PUT /api/hoxi/media/roles failed: %d %s", putRoleRec.Code, putRoleRec.Body.String())
	}
	getRoleRec := httptest.NewRecorder()
	g.mux.ServeHTTP(getRoleRec, httptest.NewRequest(http.MethodGet, "/api/hoxi/media/roles", nil))
	var roleBody struct {
		OK    bool              `json:"ok"`
		Roles map[string]string `json:"roles"`
	}
	_ = json.Unmarshal(getRoleRec.Body.Bytes(), &roleBody)
	if !roleBody.OK || roleBody.Roles["imageModel"] != "google/gemini-3-pro-image" || roleBody.Roles["videoModel"] != "google/veo-3.0-generate-001" {
		t.Fatalf("unexpected GET /api/hoxi/media/roles response: %+v", roleBody)
	}

	// Malformed specs, hoxi-auto, unknown fields, and non-string values return 400 without clobbering state
	for _, badPayload := range []string{
		`{"imageModel":"gpt-image-1"}`,
		`{"imageModel":"hoxi-auto/daily"}`,
		`{"unknownField":"openai/gpt-image-1"}`,
		`{"speechModel":123}`,
	} {
		badRec := httptest.NewRecorder()
		g.mux.ServeHTTP(badRec, httptest.NewRequest(http.MethodPut, "/api/hoxi/media/roles", bytes.NewReader([]byte(badPayload))))
		if badRec.Code != http.StatusBadRequest {
			t.Fatalf("expected HTTP 400 for malformed media role payload %s, got %d (%s)", badPayload, badRec.Code, badRec.Body.String())
		}
	}

	// Verify previously saved roles remained intact after rejected 400 payloads, and null clears a single field
	clearRec := httptest.NewRecorder()
	g.mux.ServeHTTP(clearRec, httptest.NewRequest(http.MethodPut, "/api/hoxi/media/roles", bytes.NewReader([]byte(`{"videoModel":null}`))))
	if clearRec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 clearing videoModel, got %d", clearRec.Code)
	}
	afterRec := httptest.NewRecorder()
	g.mux.ServeHTTP(afterRec, httptest.NewRequest(http.MethodGet, "/api/hoxi/media/roles", nil))
	var afterBody struct {
		Roles map[string]string `json:"roles"`
	}
	_ = json.Unmarshal(afterRec.Body.Bytes(), &afterBody)
	if afterBody.Roles["imageModel"] != "google/gemini-3-pro-image" || afterBody.Roles["videoModel"] != "" {
		t.Fatalf("expected imageModel preserved and videoModel cleared, got %+v", afterBody.Roles)
	}
}
