package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/attachment"
	"github.com/hoxiai/hoxi/internal/llm"
)

type mediaVideoRequest struct {
	Prompt          string   `json:"prompt"`
	ProviderID      string   `json:"providerID,omitempty"`
	ModelID         string   `json:"modelID,omitempty"`
	AspectRatio     string   `json:"aspectRatio,omitempty"`
	Resolution      string   `json:"resolution,omitempty"`
	DurationSeconds int      `json:"durationSeconds,omitempty"`
	SessionID       string   `json:"sessionID,omitempty"`
	Directory       string   `json:"directory,omitempty"`
	InputPaths      []string `json:"inputPaths,omitempty"`
	OutputPath      string   `json:"outputPath,omitempty"`
}

func videoExtForMime(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "video/webm":
		return ".webm"
	case "video/quicktime":
		return ".mov"
	default:
		return ".mp4"
	}
}

func (g *Gateway) handleMediaVideo(w http.ResponseWriter, r *http.Request) {
	if isRemoteRequest(r) {
		writeJSONError(w, http.StatusForbidden, "local media generation is not exposed over remote tunnel")
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
	var req mediaVideoRequest
	if isAppMediaRequest(r) {
		if !decodeAppJSON(w, r, &req, 4<<20) {
			return
		}
	} else if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}
	if isAppMediaRequest(r) && (req.Directory != "" || r.URL.Query().Has("directory") || req.OutputPath != "" || req.SessionID != "" || len(req.InputPaths) != 0) {
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

	var inputImages []llm.ImageInput
	for _, inPath := range req.InputPaths {
		if strings.TrimSpace(inPath) == "" {
			continue
		}
		safeIn, err := resolveSafeWorkspaceMediaPath(dir, inPath, true)
		if err != nil {
			writeJSONError(w, http.StatusForbidden, err.Error())
			return
		}
		raw, err := os.ReadFile(safeIn)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "cannot read input image: "+err.Error())
			return
		}
		if len(raw) > attachment.MaxAttachmentBytes {
			writeJSONError(w, http.StatusBadRequest, "input image exceeds 15 MB limit")
			return
		}
		inputImages = append(inputImages, llm.ImageInput{
			Mime: attachment.DetectFileMimeType(safeIn, raw),
			Data: raw,
		})
	}

	sessPID, sessMID := g.sessionActiveModel(req.SessionID)
	resolvedPID, resolvedMID, resolution, hasVidModel := g.providers.ResolveVideoModelForSession(
		req.ProviderID, req.ModelID, sessPID, sessMID,
	)
	if !hasVidModel {
		if writeAppMediaFailure(w, r, llm.ErrUnavailable) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":                false,
			"missingCapability": "video",
			"fallbackTool":      "hoxi_tools",
			"error":             "neither the active model nor any configured local provider supports video generation; configure a Veo, Sora, Wan, Seedance, Kling, or CogVideo model in Settings -> Providers, or fall back to hoxi_tools (discover -> inspect -> run) if cloud video synthesis APIs are available",
		})
		return
	}

	resp, err := g.providers.GenerateVideo(r.Context(), llm.VideoRequest{
		ProviderID:      resolvedPID,
		ModelID:         resolvedMID,
		Prompt:          req.Prompt,
		AspectRatio:     req.AspectRatio,
		Resolution:      req.Resolution,
		DurationSeconds: req.DurationSeconds,
		InputImages:     inputImages,
	})
	if err != nil {
		if writeAppMediaFailure(w, r, err) {
			return
		}
		if errors.Is(err, llm.ErrUnavailable) {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":                false,
				"missingCapability": "video",
				"fallbackTool":      "hoxi_tools",
				"error":             "neither the active model nor any configured local provider supports video generation; configure a Veo, Sora, Wan, Seedance, Kling, or CogVideo model in Settings -> Providers, or fall back to hoxi_tools (discover -> inspect -> run) if cloud video synthesis APIs are available",
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	if isAppMediaRequest(r) {
		items := make([]appMediaOutput, 0, len(resp.Videos))
		for _, item := range resp.Videos {
			items = append(items, appMediaOutput{Mime: item.Mime, Data: item.Data})
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "providerID": resp.ProviderID,
			"modelID": resp.ModelID, "resolution": resolution, "videos": items})
		return
	}

	nowMs := time.Now().UnixMilli()
	var attStore *attachment.Store
	if g.runner != nil {
		attStore = g.runner.Attachments()
	}

	var savedItems []mediaImageSavedItem
	for idx, vid := range resp.Videos {
		if len(vid.Data) == 0 {
			continue
		}
		ext := videoExtForMime(vid.Mime)
		fname := fmt.Sprintf("generated_video_%d_%d%s", nowMs, idx+1, ext)
		item := mediaImageSavedItem{
			Filename: fname,
			Mime:     vid.Mime,
		}
		if attStore != nil {
			if meta, saveErr := attStore.Save(attachment.SaveRequest{
				SessionID: req.SessionID,
				Directory: dir,
				Filename:  fname,
				Mime:      vid.Mime,
				Data:      vid.Data,
				MaxBytes:  attachment.MaxVideoAttachmentBytes,
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
		if resolvedOutput != "" {
			dst := resolvedOutput
			if idx > 0 {
				origExt := filepath.Ext(dst)
				stem := strings.TrimSuffix(dst, origExt)
				if origExt == "" {
					origExt = ext
				}
				dst = fmt.Sprintf("%s_%d%s", stem, idx+1, origExt)
			} else if filepath.Ext(dst) == "" {
				dst += ext
			}
			relPath, wErr := writeMediaOutputFile(dir, dst, vid.Data)
			if wErr != nil {
				writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "failed to write outputPath: " + wErr.Error()})
				return
			}
			item.Path = relPath
		}
		savedItems = append(savedItems, item)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"providerID": resp.ProviderID,
		"modelID":    resp.ModelID,
		"resolution": resolution,
		"text":       resp.Text,
		"videos":     savedItems,
	})
}
