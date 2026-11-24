package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/provider"
)

func TestMediaImageTranslateRouteAndCapabilitiesSlots(t *testing.T) {
	tinyPNG, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
	tinyDataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(tinyPNG)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bg.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(tinyPNG)
		case "/v1/images/translations":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"finalImageUrl": srv.URL + "/bg.png",
					"scene": map[string]any{
						"backgroundImg": srv.URL + "/bg.png",
						"children":      []map[string]any{{"type": "image", "src": srv.URL + "/bg.png"}},
					},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	hoxiHome := t.TempDir()
	t.Setenv("HOXI_HOME", hoxiHome)
	wsDir := t.TempDir()

	modelsDoc := map[string]any{
		"provider": map[string]any{
			"gw": map[string]any{
				"name": "Local Gateway",
				"npm":  "@ai-sdk/openai-compatible",
				"options": map[string]any{
					"baseURL": srv.URL + "/v1",
					"apiKey":  "sk-secret",
				},
				"models": map[string]any{
					"gpt-image-1": map[string]any{
						"name":           "GPT Image 1",
						"capabilityTags": []string{"image_generate", "image_edit"},
					},
					"alimt-image-translate": map[string]any{
						"name":           "AliMT Image Translate",
						"capabilityTags": []string{"image_translate"},
						"imageTranslate": map[string]any{"outputModes": []string{"raster", "editable_scene"}},
					},
				},
			},
		},
	}
	rawDoc, _ := json.Marshal(modelsDoc)
	if err := os.WriteFile(filepath.Join(hoxiHome, "models.json"), rawDoc, 0o600); err != nil {
		t.Fatal(err)
	}

	g := &Gateway{
		rootDir:   wsDir,
		mux:       http.NewServeMux(),
		providers: provider.NewService(0, "", wsDir, nil),
	}
	g.registerMediaRoutes()

	// 1. Remote tunnel requests to /api/hoxi/media/image-translate must return 403
	remoteReq := WithRemoteSource(
		httptest.NewRequest(http.MethodPost, "/api/hoxi/media/image-translate", strings.NewReader(`{"image":"`+tinyDataURL+`"}`)),
		"gopanel",
	)
	remoteRec := httptest.NewRecorder()
	g.mux.ServeHTTP(remoteRec, remoteReq)
	if remoteRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on remote image-translate, got %d", remoteRec.Code)
	}

	// 2. GET /api/hoxi/media/capabilities exposes imageEdit + imageTranslateEditor + slots
	capRec := httptest.NewRecorder()
	g.mux.ServeHTTP(capRec, httptest.NewRequest(http.MethodGet, "/api/hoxi/media/capabilities", nil))
	if capRec.Code != http.StatusOK {
		t.Fatalf("capabilities status=%d body=%s", capRec.Code, capRec.Body.String())
	}
	var capRes struct {
		OK                           bool             `json:"ok"`
		HasImageEditModel            bool             `json:"hasImageEditModel"`
		HasImageTranslateEditorModel bool             `json:"hasImageTranslateEditorModel"`
		Slots                        map[string][]any `json:"slots"`
	}
	if err := json.Unmarshal(capRec.Body.Bytes(), &capRes); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if !capRes.OK || !capRes.HasImageEditModel || !capRes.HasImageTranslateEditorModel || len(capRes.Slots["image_translate_editor"]) != 1 {
		t.Fatalf("unexpected capabilities payload: %+v", capRes)
	}

	// 3. POST /api/hoxi/media/image-translate returns scene with inlined dataUrl
	body, _ := json.Marshal(map[string]any{
		"requestId":      "req_gw_1",
		"image":          tinyDataURL,
		"sourceLanguage": "zh",
		"targetLanguage": "ru",
	})
	rec := httptest.NewRecorder()
	g.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/hoxi/media/image-translate", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from image-translate, got %d (%s)", rec.Code, rec.Body.String())
	}
	var trRes struct {
		OK            bool           `json:"ok"`
		RequestID     string         `json:"requestId"`
		FinalImageURL string         `json:"finalImageUrl"`
		Scene         map[string]any `json:"scene"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &trRes); err != nil {
		t.Fatalf("decode image-translate response: %v", err)
	}
	if !trRes.OK || trRes.RequestID != "req_gw_1" || !strings.HasPrefix(trRes.FinalImageURL, "data:image/png;base64,") {
		t.Fatalf("unexpected image-translate response: %+v", trRes)
	}
	if bg, _ := trRes.Scene["backgroundImg"].(string); !strings.HasPrefix(bg, "data:image/png;base64,") {
		t.Fatalf("expected inlined scene backgroundImg, got %q", bg)
	}

	// 3b. Bare model alias hint from embedded editor falls back to configured image_translate_editor slot
	hintBody, _ := json.Marshal(map[string]any{
		"requestId":      "req_gw_hint",
		"image":          tinyDataURL,
		"sourceLanguage": "zh",
		"targetLanguage": "en",
		"model":          "unknown-editor-alias",
	})
	hintRec := httptest.NewRecorder()
	g.mux.ServeHTTP(hintRec, httptest.NewRequest(http.MethodPost, "/api/hoxi/media/image-translate", bytes.NewReader(hintBody)))
	if hintRec.Code != http.StatusOK || !strings.Contains(hintRec.Body.String(), `"ok":true`) {
		t.Fatalf("expected bare model hint to fall back to configured editor slot, got %d (%s)", hintRec.Code, hintRec.Body.String())
	}

	// 4. PUT /api/hoxi/media/roles accepts new slot fields and rejects unknown keys
	roleRec := httptest.NewRecorder()
	g.mux.ServeHTTP(roleRec, httptest.NewRequest(http.MethodPut, "/api/hoxi/media/roles", strings.NewReader(`{"imageEditModel":"gw/gpt-image-1","imageTranslateEditorModel":"gw/alimt-image-translate"}`)))
	if roleRec.Code != http.StatusOK || !strings.Contains(roleRec.Body.String(), `"imageTranslateEditorModel":"gw/alimt-image-translate"`) {
		t.Fatalf("expected 200 saving new media role fields, got %d (%s)", roleRec.Code, roleRec.Body.String())
	}
	badRoleRec := httptest.NewRecorder()
	g.mux.ServeHTTP(badRoleRec, httptest.NewRequest(http.MethodPut, "/api/hoxi/media/roles", strings.NewReader(`{"unknownSlot":"gw/gpt-image-1"}`)))
	if badRoleRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on unknown media role field, got %d", badRoleRec.Code)
	}
}
