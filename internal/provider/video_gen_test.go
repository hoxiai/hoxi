package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

func minimalMP4Fixture() []byte {
	return []byte{
		0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'm', 'p', '4', '2',
		0x00, 0x00, 0x00, 0x00, 'm', 'p', '4', '2', 'i', 's', 'o', 'm',
	}
}

func TestSessionSelfCheckBeforeConfiguredImageAndVideoModels(t *testing.T) {
	home := t.TempDir()
	wsDir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))

	modelsCfgDir := filepath.Join(wsDir, ".hoxi")
	if err := os.MkdirAll(modelsCfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgDoc := map[string]any{
		"provider": map[string]any{
			"omni-hub": map[string]any{
				"name": "Omni Hub",
				"npm":  "@ai-sdk/openai-compatible",
				"options": map[string]any{
					"baseURL": "https://api.omni.example/v1",
					"apiKey":  "sk-omni-test",
				},
				"models": map[string]any{
					// Conversational omni model that ALSO supports native image + video output:
					// IsImageModel / IsVideoModel must stay false so coding turns work, while
					// CanModelGenerateImage / CanModelGenerateVideo must return true so self-check
					// uses the active model itself ("session_self").
					"omni-pro": map[string]any{
						"name":      "Omni Pro",
						"category":  "chat",
						"tool_call": true,
						"modalities": map[string]any{
							"input":  []string{"text", "image", "video"},
							"output": []string{"text", "image", "video"},
						},
					},
					"plain-coder": map[string]any{
						"name":      "Plain Coder",
						"category":  "coding",
						"tool_call": true,
						"modalities": map[string]any{
							"input":  []string{"text"},
							"output": []string{"text"},
						},
					},
				},
			},
			"media-dedicated": map[string]any{
				"name": "Media Dedicated",
				"npm":  "@ai-sdk/google",
				"options": map[string]any{
					"baseURL": "https://generativelanguage.googleapis.com/v1beta",
					"apiKey":  "AIzaSyTestKey12345",
				},
				"models": map[string]any{
					"gemini-3-pro-image": map[string]any{
						"name":     "Gemini 3 Pro Image",
						"category": "image",
					},
					"veo-3.0-generate-001": map[string]any{
						"name":     "Veo 3",
						"category": "video",
					},
				},
			},
		},
	}
	rawCfg, _ := json.MarshalIndent(cfgDoc, "", "  ")
	if err := os.WriteFile(filepath.Join(modelsCfgDir, "models.json"), rawCfg, 0o600); err != nil {
		t.Fatal(err)
	}

	svc := NewService(0, "", wsDir, nil)

	if svc.IsImageModel("omni-hub", "omni-pro") || svc.IsVideoModel("omni-hub", "omni-pro") {
		t.Fatal("conversational category='chat' omni model must not be classified as dedicated-only media model")
	}
	if !svc.CanModelGenerateImage("omni-hub", "omni-pro") || !svc.CanModelGenerateVideo("omni-hub", "omni-pro") {
		t.Fatal("expected omni-pro with output:['text','image','video'] to pass CanModelGenerateImage/Video")
	}

	// 1. Active session uses omni-pro -> self-check picks omni-pro itself ("session_self")
	// even though media-dedicated has higher-scored gemini-3-pro-image and veo-3.0-generate-001.
	imgPID, imgMID, imgRes, ok := svc.ResolveImageModelForSession("", "", "omni-hub", "omni-pro")
	if !ok || imgPID != "omni-hub" || imgMID != "omni-pro" || imgRes != "session_self" {
		t.Fatalf("expected image self-check to pick omni-hub/omni-pro (session_self), got %s/%s (%s) ok=%v", imgPID, imgMID, imgRes, ok)
	}
	vidPID, vidMID, vidRes, ok := svc.ResolveVideoModelForSession("", "", "omni-hub", "omni-pro")
	if !ok || vidPID != "omni-hub" || vidMID != "omni-pro" || vidRes != "session_self" {
		t.Fatalf("expected video self-check to pick omni-hub/omni-pro (session_self), got %s/%s (%s) ok=%v", vidPID, vidMID, vidRes, ok)
	}

	// 2. Active session uses plain-coder (text-only output) -> self-check sees plain-coder
	// cannot output image/video and falls back to configured dedicated models ("configured_model").
	imgPID, imgMID, imgRes, ok = svc.ResolveImageModelForSession("", "", "omni-hub", "plain-coder")
	if !ok || imgPID != "media-dedicated" || imgMID != "gemini-3-pro-image" || imgRes != "configured_model" {
		t.Fatalf("expected fallback to configured image model gemini-3-pro-image, got %s/%s (%s) ok=%v", imgPID, imgMID, imgRes, ok)
	}
	vidPID, vidMID, vidRes, ok = svc.ResolveVideoModelForSession("", "", "omni-hub", "plain-coder")
	if !ok || vidPID != "media-dedicated" || vidMID != "veo-3.0-generate-001" || vidRes != "configured_model" {
		t.Fatalf("expected fallback to configured video model veo-3.0-generate-001, got %s/%s (%s) ok=%v", vidPID, vidMID, vidRes, ok)
	}

	// 3. Corrupt media-roles.json is backed up safely; setting role_default overrides auto-score
	// on non-native chat sessions while session_self still wins when active model is native!
	rolesPath := svc.mediaRolesFilePath()
	_ = os.MkdirAll(filepath.Dir(rolesPath), 0o700)
	_ = os.WriteFile(rolesPath, []byte("{corrupt-json"), 0o600)
	if got := svc.LoadMediaRolePrefs(); got.ImageModel != "" {
		t.Fatalf("expected empty prefs after corrupt backup, got %+v", got)
	}
	matches, _ := filepath.Glob(rolesPath + ".corrupt-*")
	if len(matches) == 0 {
		t.Fatal("expected corrupt media-roles.json to be backed up before reset")
	}

	customImg := "omni-hub/omni-pro"
	if _, err := svc.UpdateMediaRolePrefs(map[string]*string{"imageModel": &customImg}); err != nil {
		t.Fatalf("UpdateMediaRolePrefs failed: %v", err)
	}
	imgPID, imgMID, imgRes, ok = svc.ResolveImageModelForSession("", "", "omni-hub", "plain-coder")
	if !ok || imgPID != "omni-hub" || imgMID != "omni-pro" || imgRes != "role_default" {
		t.Fatalf("expected role_default omni-hub/omni-pro when plain-coder active, got %s/%s (%s)", imgPID, imgMID, imgRes)
	}
	// Active native model still wins over role_default
	imgPID, imgMID, imgRes, ok = svc.ResolveImageModelForSession("", "", "media-dedicated", "gemini-3-pro-image")
	if !ok || imgPID != "media-dedicated" || imgMID != "gemini-3-pro-image" || imgRes != "session_self" {
		t.Fatalf("expected session_self to take precedence over role_default, got %s/%s (%s)", imgPID, imgMID, imgRes)
	}
}

func TestSynthesizeSpeechAcrossGeminiPCMAndOpenAI(t *testing.T) {
	pcmSamples := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	mp3Fake := []byte("ID3-fake-mp3-payload")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1beta/models/gemini-2.5-flash-preview-tts:generateContent":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"candidates": []map[string]any{
					{
						"content": map[string]any{
							"parts": []map[string]any{
								{
									"inlineData": map[string]any{
										"mimeType": "audio/L16;codec=pcm;rate=24000",
										"data":     base64.StdEncoding.EncodeToString(pcmSamples),
									},
								},
							},
						},
					},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/audio/speech":
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write(mp3Fake)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	home := t.TempDir()
	wsDir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))

	modelsCfgDir := filepath.Join(wsDir, ".hoxi")
	_ = os.MkdirAll(modelsCfgDir, 0o755)
	cfgDoc := map[string]any{
		"provider": map[string]any{
			"google-tts": map[string]any{
				"name": "Google TTS",
				"npm":  "@ai-sdk/google",
				"options": map[string]any{
					"baseURL": srv.URL + "/v1beta",
					"apiKey":  "AIzaTTSTestKey",
				},
				"models": map[string]any{
					"gemini-2.5-flash-preview-tts": map[string]any{"name": "Gemini TTS", "category": "tts"},
				},
			},
			"oai-tts": map[string]any{
				"name": "OpenAI TTS",
				"npm":  "@ai-sdk/openai-compatible",
				"options": map[string]any{
					"baseURL": srv.URL + "/v1",
					"apiKey":  "sk-tts-key",
				},
				"models": map[string]any{
					"gpt-4o-mini-tts": map[string]any{"name": "GPT-4o Mini TTS", "category": "tts"},
				},
			},
		},
	}
	rawCfg, _ := json.Marshal(cfgDoc)
	_ = os.WriteFile(filepath.Join(modelsCfgDir, "models.json"), rawCfg, 0o600)

	svc := NewService(0, "", wsDir, nil)
	if len(svc.ListSpeechModels()) != 2 {
		t.Fatalf("expected 2 speech models, got %+v", svc.ListSpeechModels())
	}

	gemRes, err := svc.SynthesizeSpeech(context.Background(), llm.SpeechRequest{
		ProviderID: "google-tts",
		ModelID:    "gemini-2.5-flash-preview-tts",
		Input:      "Welcome to Hoxi",
		Voice:      "Kore",
	})
	if err != nil || gemRes.Mime != "audio/wav" || len(gemRes.Data) != 44+len(pcmSamples) || string(gemRes.Data[:4]) != "RIFF" {
		t.Fatalf("expected Gemini PCM to wrap into RIFF WAV, err=%v mime=%s len=%d", err, gemRes.Mime, len(gemRes.Data))
	}

	oaiRes, err := svc.SynthesizeSpeech(context.Background(), llm.SpeechRequest{
		ProviderID: "oai-tts",
		ModelID:    "gpt-4o-mini-tts",
		Input:      "Hello world",
		Voice:      "alloy",
	})
	if err != nil || oaiRes.Mime != "audio/mpeg" || string(oaiRes.Data) != string(mp3Fake) {
		t.Fatalf("OpenAI SynthesizeSpeech failed: err=%v res=%+v", err, oaiRes)
	}
}

func TestGenerateVideoAcrossVeoAndOpenAIWithSSRFGuard(t *testing.T) {
	origPoll := videoPollIntervalOverride
	videoPollIntervalOverride = 10 * time.Millisecond
	t.Cleanup(func() {
		videoPollIntervalOverride = origPoll
	})

	mp4Bytes := minimalMP4Fixture()
	var veoPolls atomic.Int32
	var oaiPolls atomic.Int32

	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1beta/models/veo-3.0-generate-001":
			if r.Header.Get("x-goog-api-key") != "AIzaVeoTestKey" {
				http.Error(w, `{"error":{"message":"missing google key"}}`, http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"name":"models/veo-3.0-generate-001"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1beta/models/veo-3.0-generate-001:predictLongRunning":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"name":"models/veo-3.0-generate-001/operations/op_123","done":false}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1beta/models/veo-3.0-generate-001/operations/op_123":
			n := veoPolls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			if n < 2 {
				_, _ = w.Write([]byte(`{"name":"models/veo-3.0-generate-001/operations/op_123","done":false}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name": "models/veo-3.0-generate-001/operations/op_123",
				"done": true,
				"response": map[string]any{
					"generateVideoResponse": map[string]any{
						"generatedSamples": []map[string]any{
							{"video": map[string]any{"uri": srvURL + "/v1beta/files/vid_veo.mp4"}},
						},
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1beta/files/vid_veo.mp4":
			if r.Header.Get("x-goog-api-key") != "AIzaVeoTestKey" {
				http.Error(w, "missing key on google download", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(mp4Bytes)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/videos":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"vid_job_1","status":"in_progress"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/videos/vid_job_1":
			n := oaiPolls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			if n < 2 {
				_, _ = w.Write([]byte(`{"id":"vid_job_1","status":"in_progress"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"vid_job_1","status":"completed"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/videos/vid_job_1/content":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(mp4Bytes)
		case r.Method == http.MethodPost && r.URL.Path == "/chat-only/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			dataURI := "data:video/mp4;base64," + base64.StdEncoding.EncodeToString(mp4Bytes)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{"message": map[string]any{"content": "Generated clip: " + dataURI}},
				},
			})
		case r.URL.Path == "/chat-only/videos" || r.URL.Path == "/chat-only/videos/generations":
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	home := t.TempDir()
	wsDir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))

	modelsCfgDir := filepath.Join(wsDir, ".hoxi")
	_ = os.MkdirAll(modelsCfgDir, 0o755)
	cfgDoc := map[string]any{
		"provider": map[string]any{
			"google-veo": map[string]any{
				"name": "Google Veo",
				"npm":  "@ai-sdk/google",
				"options": map[string]any{
					"baseURL": srv.URL + "/v1beta",
					"apiKey":  "AIzaVeoTestKey",
				},
				"models": map[string]any{
					"veo-3.0-generate-001": map[string]any{"name": "Veo 3", "category": "video"},
				},
			},
			"oai-sora": map[string]any{
				"name": "OpenAI Sora",
				"npm":  "@ai-sdk/openai-compatible",
				"options": map[string]any{
					"baseURL": srv.URL + "/v1",
					"apiKey":  "sk-sora-key",
				},
				"models": map[string]any{
					"sora-2": map[string]any{"name": "Sora 2", "category": "video"},
				},
			},
			"omni-chat-vid": map[string]any{
				"name": "Omni Chat Video",
				"npm":  "@ai-sdk/openai-compatible",
				"options": map[string]any{
					"baseURL": srv.URL + "/chat-only",
					"apiKey":  "sk-omni-vid",
				},
				"models": map[string]any{
					"omni-v1": map[string]any{
						"name":     "Omni v1",
						"category": "chat",
						"modalities": map[string]any{
							"input":  []string{"text", "image"},
							"output": []string{"text", "video"},
						},
					},
				},
			},
		},
	}
	rawCfg, _ := json.Marshal(cfgDoc)
	_ = os.WriteFile(filepath.Join(modelsCfgDir, "models.json"), rawCfg, 0o600)

	svc := NewService(0, "", wsDir, nil)

	// Probe Google Veo model
	if err := svc.probeVideoModel(context.Background(), imageGenConn{
		ProviderID: "google-veo",
		ModelID:    "veo-3.0-generate-001",
		BaseURL:    srv.URL + "/v1beta",
		APIKey:     "AIzaVeoTestKey",
		NPM:        "@ai-sdk/google",
	}); err != nil {
		t.Fatalf("probeVideoModel failed: %v", err)
	}

	// Generate via Google Veo :predictLongRunning + polling + authenticated URI download
	veoOut, err := svc.GenerateVideo(context.Background(), llm.VideoRequest{
		ProviderID:      "google-veo",
		ModelID:         "veo-3.0-generate-001",
		Prompt:          "Ocean waves crashing on basalt cliffs at golden hour",
		AspectRatio:     "16:9",
		DurationSeconds: 6,
	})
	if err != nil || len(veoOut.Videos) != 1 || veoOut.Videos[0].Mime != "video/mp4" {
		t.Fatalf("Veo GenerateVideo failed: err=%v out=%+v", err, veoOut)
	}

	// Generate via OpenAI /v1/videos async job + polling + /content download
	soraOut, err := svc.GenerateVideo(context.Background(), llm.VideoRequest{
		ProviderID:      "oai-sora",
		ModelID:         "sora-2",
		Prompt:          "Aerial drone shot over neon Tokyo",
		AspectRatio:     "9:16",
		Resolution:      "1080p",
		DurationSeconds: 8,
	})
	if err != nil || len(soraOut.Videos) != 1 || string(soraOut.Videos[0].Data) != string(mp4Bytes) {
		t.Fatalf("Sora GenerateVideo failed: err=%v out=%+v", err, soraOut)
	}

	// Generate via conversational chat-completions fallback when /videos returns 404
	omniOut, err := svc.GenerateVideo(context.Background(), llm.VideoRequest{
		ProviderID: "omni-chat-vid",
		ModelID:    "omni-v1",
		Prompt:     "Minimal looping loader animation",
	})
	if err != nil || len(omniOut.Videos) != 1 || omniOut.Videos[0].Mime != "video/mp4" {
		t.Fatalf("Omni chat video fallback failed: err=%v out=%+v", err, omniOut)
	}

	// Security refusal path: remote non-loopback provider returning a loopback or link-local video download URL MUST be rejected
	remoteConn := imageGenConn{
		ProviderID: "remote-evil",
		ModelID:    "sora-2",
		BaseURL:    "https://evil-gateway.example/v1",
		APIKey:     "sk-remote",
		Headers:    map[string]string{"X-Custom-Token": "tok-secret"},
	}
	_, ssrfErr := fetchGeneratedVideoURL(context.Background(), "http://127.0.0.1:5410/api/hoxi/secrets", remoteConn, false)
	if ssrfErr == nil || !strings.Contains(ssrfErr.Error(), "loopback") {
		t.Fatalf("expected SSRF refusal for loopback URL from remote provider, got: %v", ssrfErr)
	}
	_, linkLocalErr := fetchGeneratedVideoURL(context.Background(), "http://169.254.169.254/latest/meta-data/", remoteConn, false)
	if linkLocalErr == nil || !strings.Contains(linkLocalErr.Error(), "link-local") {
		t.Fatalf("expected SSRF refusal for link-local URL, got: %v", linkLocalErr)
	}

	// Redirect SSRF refusal & cross-origin auth header scrubbing
	safeClient := newSafeVideoHTTPClient(10*time.Second, "https://evil-gateway.example/v1/dl", remoteConn)
	origReq, _ := http.NewRequest(http.MethodGet, "https://evil-gateway.example/v1/dl", nil)
	applyImageAuthHeaders(origReq, remoteConn, true, true)

	redirectLoopbackReq, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:5410/api/hoxi/secrets", nil)
	if rErr := safeClient.CheckRedirect(redirectLoopbackReq, []*http.Request{origReq}); rErr == nil || !strings.Contains(rErr.Error(), "loopback") {
		t.Fatalf("expected CheckRedirect to block 302 to loopback, got: %v", rErr)
	}
	redirectMetaReq, _ := http.NewRequest(http.MethodGet, "http://169.254.169.254/latest/meta-data/", nil)
	if rErr := safeClient.CheckRedirect(redirectMetaReq, []*http.Request{origReq}); rErr == nil || !strings.Contains(rErr.Error(), "link-local") {
		t.Fatalf("expected CheckRedirect to block 302 to link-local metadata IP, got: %v", rErr)
	}

	crossOriginCDNReq, _ := http.NewRequest(http.MethodGet, "https://cdn-bucket.example.org/presigned.mp4?sig=1", nil)
	crossOriginCDNReq.Header = origReq.Header.Clone()
	if rErr := safeClient.CheckRedirect(crossOriginCDNReq, []*http.Request{origReq}); rErr != nil {
		t.Fatalf("expected cross-origin public CDN redirect to be allowed after header scrub, got: %v", rErr)
	}
	for _, hName := range []string{"Authorization", "x-goog-api-key", "x-api-key", "X-Custom-Token"} {
		if got := crossOriginCDNReq.Header.Get(hName); got != "" {
			t.Fatalf("expected %s header stripped on cross-origin redirect, still got %q", hName, got)
		}
	}

	// Cross-origin Veo operation poll URL refusal
	crossPollSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"https://other-host.example/v1beta/operations/op_evil","done":false}`))
	}))
	defer crossPollSrv.Close()
	_, pollErr := svc.googleVeoGenerateVideo(context.Background(), imageGenConn{
		ProviderID: "google-veo",
		ModelID:    "veo-3.0-generate-001",
		BaseURL:    crossPollSrv.URL + "/v1beta",
		APIKey:     "AIzaVeoTestKey",
		NPM:        "@ai-sdk/google",
	}, "test prompt", nil, videoGenExecOptions{})
	if pollErr == nil || !strings.Contains(pollErr.Error(), "cross-origin") {
		t.Fatalf("expected cross-origin Veo operation poll URL refusal, got: %v", pollErr)
	}
}
