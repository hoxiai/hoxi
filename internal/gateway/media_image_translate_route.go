package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/hoxiai/hoxi/internal/attachment"
	"github.com/hoxiai/hoxi/internal/llm"
)

type mediaImageTranslateRequest struct {
	RequestID      string `json:"requestId,omitempty"`
	Image          string `json:"image"`
	SourceLanguage string `json:"sourceLanguage,omitempty"`
	TargetLanguage string `json:"targetLanguage,omitempty"`
	ProviderID     string `json:"providerID,omitempty"`
	Model          string `json:"model,omitempty"`
	ModelID        string `json:"modelID,omitempty"`
	OutputMode     string `json:"outputMode,omitempty"`
}

func (g *Gateway) handleMediaImageTranslate(w http.ResponseWriter, r *http.Request) {
	if isRemoteRequest(r) {
		writeJSONError(w, http.StatusForbidden, "local image translation is not exposed over remote tunnel")
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

	var req mediaImageTranslateRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 36<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}

	image := strings.TrimSpace(req.Image)
	if image == "" {
		writeJSONError(w, http.StatusBadRequest, "image is required")
		return
	}
	if strings.HasPrefix(strings.ToLower(image), "data:") {
		parsedMime, decoded, decErr := attachment.DecodeDataURL(image, "")
		if decErr != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid image dataUrl: "+decErr.Error())
			return
		}
		if !strings.HasPrefix(strings.ToLower(parsedMime), "image/") {
			writeJSONError(w, http.StatusBadRequest, "image must be an image/* dataUrl or https URL")
			return
		}
		if len(decoded) > attachment.MaxAttachmentBytes {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "image exceeds 15 MB limit")
			return
		}
	} else if !strings.HasPrefix(strings.ToLower(image), "https://") {
		writeJSONError(w, http.StatusBadRequest, "image must be a data:image/* dataUrl or public https:// URL")
		return
	}

	mode := llm.ImageTranslateModeEditableScene
	if parsedMode, ok := llm.ParseImageTranslateOutputMode(req.OutputMode); ok {
		mode = parsedMode
	}

	preferredModel := strings.TrimSpace(req.ModelID)
	if preferredModel == "" {
		preferredModel = strings.TrimSpace(req.Model)
	}
	preferredPID := strings.TrimSpace(req.ProviderID)

	resolvedPID, resolvedMID, resolution, hasModel := g.providers.ResolveImageTranslateModel(mode, preferredPID, preferredModel)
	// When an embedded editor sends a default bare model hint (without providerID)
	// that isn't configured under that exact alias on the user's gateway, fall back
	// to the user's configured slot model rather than falsely reporting missingCapability.
	if !hasModel && preferredPID == "" && preferredModel != "" && !strings.Contains(preferredModel, "/") {
		resolvedPID, resolvedMID, resolution, hasModel = g.providers.ResolveImageTranslateModel(mode, "", "")
	}
	if !hasModel {
		missingCap := string(llm.ModelSlotImageTranslateEditor)
		if mode == llm.ImageTranslateModeRaster {
			missingCap = string(llm.ModelSlotImageTranslateRaster)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":                false,
			"requestId":         strings.TrimSpace(req.RequestID),
			"missingCapability": missingCap,
			"error":             "no configured local provider supports image translation for slot " + missingCap,
		})
		return
	}

	type candEntry struct {
		providerID string
		modelID    string
		resolution string
	}
	candidates := []candEntry{{providerID: resolvedPID, modelID: resolvedMID, resolution: resolution}}
	if resolution != "explicit" {
		seen := map[string]bool{strings.ToLower(resolvedPID + "/" + resolvedMID): true}
		for _, opt := range g.providers.ListImageTranslateModels(mode) {
			if len(candidates) >= 4 {
				break
			}
			k := strings.ToLower(strings.TrimSpace(opt.ProviderID) + "/" + strings.TrimSpace(opt.ModelID))
			if seen[k] || strings.TrimSpace(opt.ProviderID) == "" || strings.TrimSpace(opt.ModelID) == "" {
				continue
			}
			seen[k] = true
			candidates = append(candidates, candEntry{
				providerID: opt.ProviderID,
				modelID:    opt.ModelID,
				resolution: "configured_model_fallback",
			})
		}
	}

	var resp llm.ImageTranslateResponse
	var err error
	allUnsupported := true
	for _, cand := range candidates {
		resp, err = g.providers.TranslateImage(r.Context(), llm.ImageTranslateRequest{
			ProviderID:     cand.providerID,
			ModelID:        cand.modelID,
			Image:          image,
			SourceLanguage: strings.TrimSpace(req.SourceLanguage),
			TargetLanguage: strings.TrimSpace(req.TargetLanguage),
			OutputMode:     mode,
			RequestID:      strings.TrimSpace(req.RequestID),
		})
		if err == nil {
			resp.Resolution = cand.resolution
			allUnsupported = false
			break
		}
		if resolution == "explicit" || (!errors.Is(err, llm.ErrUnavailable) && !isUnsupportedImageModelError(err)) {
			allUnsupported = false
			break
		}
	}
	if err != nil {
		if errors.Is(err, llm.ErrUnavailable) || (resolution != "explicit" && allUnsupported) {
			missingCap := string(llm.ModelSlotImageTranslateEditor)
			if mode == llm.ImageTranslateModeRaster {
				missingCap = string(llm.ModelSlotImageTranslateRaster)
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":                false,
				"requestId":         strings.TrimSpace(req.RequestID),
				"missingCapability": missingCap,
				"error":             err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        false,
			"requestId": strings.TrimSpace(req.RequestID),
			"error":     err.Error(),
		})
		return
	}

	out := map[string]any{
		"ok":         true,
		"requestId":  strings.TrimSpace(req.RequestID),
		"providerID": resp.ProviderID,
		"modelID":    resp.ModelID,
		"resolution": resp.Resolution,
	}
	if resp.FinalImageURL != "" {
		out["finalImageUrl"] = resp.FinalImageURL
	}
	if resp.InPaintingURL != "" {
		out["inPaintingUrl"] = resp.InPaintingURL
	}
	if len(resp.Scene) > 0 && string(resp.Scene) != "null" {
		out["scene"] = resp.Scene
	}
	writeJSON(w, http.StatusOK, out)
}
