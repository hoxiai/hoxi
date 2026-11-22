package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/attachment"
	"github.com/hoxiai/hoxi/internal/llm"
)

type mediaSpeechRequest struct {
	Text       string  `json:"text"`
	Prompt     string  `json:"prompt,omitempty"`
	ProviderID string  `json:"providerID,omitempty"`
	ModelID    string  `json:"modelID,omitempty"`
	Voice      string  `json:"voice,omitempty"`
	Format     string  `json:"format,omitempty"`
	Speed      float64 `json:"speed,omitempty"`
	SessionID  string  `json:"sessionID,omitempty"`
	Directory  string  `json:"directory,omitempty"`
	OutputPath string  `json:"outputPath,omitempty"`
}

func speechExtForMime(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "audio/wav", "audio/x-wav", "audio/wave":
		return ".wav"
	case "audio/ogg", "audio/opus":
		return ".ogg"
	case "audio/flac":
		return ".flac"
	case "audio/aac":
		return ".aac"
	default:
		return ".mp3"
	}
}

func (g *Gateway) handleMediaSpeech(w http.ResponseWriter, r *http.Request) {
	if isRemoteRequest(r) {
		writeJSONError(w, http.StatusForbidden, "local speech synthesis is not exposed over remote tunnel")
		return
	}
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	if g.providers == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "provider service is unavailable")
		return
	}
	var req mediaSpeechRequest
	if isAppMediaRequest(r) {
		if !decodeAppJSON(w, r, &req, 4<<20) {
			return
		}
	} else if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}
	inputText := strings.TrimSpace(req.Text)
	if inputText == "" {
		inputText = strings.TrimSpace(req.Prompt)
	}
	if isAppMediaRequest(r) && (req.Directory != "" || r.URL.Query().Has("directory") || req.OutputPath != "" || req.SessionID != "") {
		writeAppError(w, http.StatusForbidden, "FILES_FORBIDDEN", "application media endpoints do not access local files or desktop sessions")
		return
	}

	dir := strings.TrimSpace(req.Directory)
	if dir == "" {
		dir = strings.TrimSpace(r.URL.Query().Get("directory"))
	}
	if dir == "" {
		dir = strings.TrimSpace(g.rootDir)
	}

	var resolvedOutput string
	if strings.TrimSpace(req.OutputPath) != "" {
		if dir == "" {
			writeJSONError(w, http.StatusBadRequest, "workspace directory is required when outputPath is set")
			return
		}
		var err error
		resolvedOutput, err = resolveSafeWorkspaceMediaPath(dir, req.OutputPath, true)
		if err != nil {
			writeJSONError(w, http.StatusForbidden, err.Error())
			return
		}
	}

	sessPID, sessMID := g.sessionActiveModel(req.SessionID)
	resolvedPID, resolvedMID, resolution, hasSpeechModel := g.providers.ResolveSpeechModelForSession(
		req.ProviderID, req.ModelID, sessPID, sessMID,
	)
	if !hasSpeechModel {
		if writeAppMediaFailure(w, r, llm.ErrUnavailable) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":                false,
			"missingCapability": "speech",
			"fallbackTool":      "hoxi_tools",
			"error":             "neither the active model nor any configured local provider supports speech synthesis; configure a TTS model (OpenAI gpt-4o-mini-tts / tts-1, Gemini TTS, CosyVoice, ElevenLabs, FishAudio) in Settings -> Providers, or fall back to hoxi_tools (elevenlabs / fishaudio)",
		})
		return
	}

	resp, err := g.providers.SynthesizeSpeech(r.Context(), llm.SpeechRequest{
		ProviderID: resolvedPID,
		ModelID:    resolvedMID,
		Input:      inputText,
		Voice:      req.Voice,
		Format:     req.Format,
		Speed:      req.Speed,
	})
	if err != nil {
		if writeAppMediaFailure(w, r, err) {
			return
		}
		if errors.Is(err, llm.ErrUnavailable) {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":                false,
				"missingCapability": "speech",
				"fallbackTool":      "hoxi_tools",
				"error":             "neither the active model nor any configured local provider supports speech synthesis; configure a TTS model (OpenAI gpt-4o-mini-tts / tts-1, Gemini TTS, CosyVoice, ElevenLabs, FishAudio) in Settings -> Providers, or fall back to hoxi_tools (elevenlabs / fishaudio)",
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	if isAppMediaRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "providerID": resp.ProviderID,
			"modelID": resp.ModelID, "resolution": resolution, "audios": []appMediaOutput{{Mime: resp.Mime, Data: resp.Data}}})
		return
	}

	ext := speechExtForMime(resp.Mime)
	fname := fmt.Sprintf("generated_speech_%d%s", time.Now().UnixMilli(), ext)
	item := mediaImageSavedItem{
		Filename: fname,
		Mime:     resp.Mime,
	}
	if g.runner != nil && g.runner.Attachments() != nil && len(resp.Data) > 0 {
		attStore := g.runner.Attachments()
		if meta, saveErr := attStore.Save(attachment.SaveRequest{
			SessionID: req.SessionID,
			Directory: dir,
			Filename:  fname,
			Mime:      resp.Mime,
			Data:      resp.Data,
			MaxBytes:  32 * 1024 * 1024,
		}); saveErr == nil {
			item.URL = meta.URL
			item.Filename = meta.Filename
			item.Mime = meta.Mime
			if resolvedOutput == "" && dir != "" {
				if _, matPath, matErr := attStore.MaterializeToDir(meta.ID, dir); matErr == nil {
					if rel, relErr := filepath.Rel(dir, matPath); relErr == nil {
						item.Path = filepath.ToSlash(rel)
					} else {
						item.Path = matPath
					}
				}
			}
		}
	}
	if resolvedOutput != "" && len(resp.Data) > 0 {
		dst := resolvedOutput
		if filepath.Ext(dst) == "" {
			dst += ext
		}
		relPath, wErr := writeMediaOutputFile(dir, dst, resp.Data)
		if wErr != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "failed to write outputPath: " + wErr.Error()})
			return
		}
		item.Path = relPath
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"providerID": resp.ProviderID,
		"modelID":    resp.ModelID,
		"resolution": resolution,
		"audios":     []mediaImageSavedItem{item},
	})
}
