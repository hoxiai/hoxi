package gateway

import (
	"encoding/base64"
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
	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/llm"
)

const maxLocalMediaUploadBytes = 25 << 20

func (g *Gateway) registerMediaRoutes() {
	g.mux.HandleFunc("/api/hoxi/media/capabilities", g.handleMediaCapabilities)
	g.mux.HandleFunc("/api/hoxi/media/roles", g.handleMediaRoles)
	g.mux.HandleFunc("/api/hoxi/media/image", g.handleMediaImage)
	g.mux.HandleFunc("/api/hoxi/media/video", g.handleMediaVideo)
	g.mux.HandleFunc("/api/hoxi/media/speech", g.handleMediaSpeech)
	g.mux.HandleFunc("/api/hoxi/media/transcribe", g.handleMediaTranscribe)
}

func (g *Gateway) sessionActiveModel(sessionID string) (string, string) {
	if g == nil || g.runner == nil || strings.TrimSpace(sessionID) == "" {
		return "", ""
	}
	return g.runner.SessionModel(sessionID)
}

func (g *Gateway) handleMediaCapabilities(w http.ResponseWriter, r *http.Request) {
	if isRemoteRequest(r) {
		writeJSONError(w, http.StatusForbidden, "local media capabilities are not exposed over remote tunnel")
		return
	}
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	var imageModels []llm.ImageModelOption
	var videoModels []llm.VideoModelOption
	var speechModels []llm.AudioModelOption
	var transcribeModels []llm.AudioModelOption
	var roles llm.MediaRolePrefs
	hasASR := false
	hasEmbedding := false
	if g.providers != nil {
		imageModels = g.providers.ListImageModels()
		videoModels = g.providers.ListVideoModels()
		speechModels = g.providers.ListSpeechModels()
		transcribeModels = g.providers.ListAudioTranscriberModels()
		roles = g.providers.LoadMediaRolePrefs()
		hasASR = g.providers.HasAudioTranscriber()
		hasEmbedding = g.providers.HasEmbeddingModel()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                  true,
		"hasImageModel":       len(imageModels) > 0,
		"hasVideoModel":       len(videoModels) > 0,
		"hasSpeechModel":      len(speechModels) > 0,
		"hasAudioTranscriber": hasASR,
		"hasEmbeddingModel":   hasEmbedding,
		"imageModels":         imageModels,
		"videoModels":         videoModels,
		"speechModels":        speechModels,
		"transcribeModels":    transcribeModels,
		"roles":               roles,
	})
}

type mediaImageRequest struct {
	Prompt        string   `json:"prompt"`
	ProviderID    string   `json:"providerID,omitempty"`
	ModelID       string   `json:"modelID,omitempty"`
	AspectRatio   string   `json:"aspectRatio,omitempty"`
	ImageSize     string   `json:"imageSize,omitempty"`
	Count         int      `json:"count,omitempty"`
	SessionID     string   `json:"sessionID,omitempty"`
	Directory     string   `json:"directory,omitempty"`
	InputPaths    []string `json:"inputPaths,omitempty"`
	InputDataURLs []string `json:"inputDataUrls,omitempty"`
	MaskDataURL   string   `json:"maskDataUrl,omitempty"`
	OutputPath    string   `json:"outputPath,omitempty"`
	ReturnDataURL bool     `json:"returnDataUrl,omitempty"`
}

type mediaImageSavedItem struct {
	Filename string `json:"filename"`
	Mime     string `json:"mime"`
	URL      string `json:"url,omitempty"`
	Path     string `json:"path,omitempty"`
	DataURL  string `json:"dataUrl,omitempty"`
}

func checkWorkspaceRelMediaRules(slashRel, target string, allowAttachmentSubdir bool) error {
	if allowAttachmentSubdir && strings.HasPrefix(slashRel, ".hoxi/attachments/") {
		return nil
	}
	if hoxipaths.IsDeniedDocOrSpreadsheetPath(target) {
		return errors.New("refusing to access protected or credential path")
	}
	lowerRel := strings.ToLower(slashRel)
	for _, prefix := range []string{".git/", ".opencode/", ".claude/", ".hoxi/", ".agents/", ".gemini/"} {
		if strings.HasPrefix(lowerRel, prefix) || lowerRel == strings.TrimSuffix(prefix, "/") {
			return errors.New("refusing to access protected dot-directory")
		}
	}
	baseName := strings.ToLower(filepath.Base(target))
	if baseName == ".env" || strings.HasPrefix(baseName, ".env.") || baseName == ".envrc" {
		return errors.New("refusing to access secret environment file")
	}
	return nil
}

func evalExistingAncestorSymlinks(absPath string) (string, error) {
	cur := filepath.Clean(absPath)
	var suffix []string
	for {
		_, err := os.Lstat(cur)
		if err == nil {
			resolved, evalErr := filepath.EvalSymlinks(cur)
			if evalErr != nil {
				return "", errors.New("refusing broken or unresolvable symlink path")
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return filepath.Clean(absPath), nil
		}
		suffix = append(suffix, filepath.Base(cur))
		cur = parent
	}
}

func resolveSafeWorkspaceMediaPath(worktreeDir, relOrAbs string, allowAttachmentSubdir bool) (string, error) {
	base := filepath.Clean(strings.TrimSpace(worktreeDir))
	if base == "" || !filepath.IsAbs(base) {
		return "", errors.New("workspace directory must be an absolute path")
	}
	raw := strings.TrimSpace(relOrAbs)
	if raw == "" {
		return "", errors.New("file path is empty")
	}
	for _, seg := range strings.FieldsFunc(raw, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return "", errors.New("path traversal is not allowed")
		}
	}
	target := raw
	if !filepath.IsAbs(target) {
		target = filepath.Join(base, target)
	}
	target = filepath.Clean(target)
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("target path must stay inside the workspace directory")
	}
	if err := checkWorkspaceRelMediaRules(filepath.ToSlash(rel), target, allowAttachmentSubdir); err != nil {
		return "", err
	}

	realBase := base
	if evalBase, bErr := filepath.EvalSymlinks(base); bErr == nil && strings.TrimSpace(evalBase) != "" {
		realBase = filepath.Clean(evalBase)
	}
	realTarget, symErr := evalExistingAncestorSymlinks(target)
	if symErr != nil {
		return "", symErr
	}
	realRel, relErr := filepath.Rel(realBase, realTarget)
	if relErr != nil || realRel == "." || realRel == ".." || strings.HasPrefix(realRel, ".."+string(filepath.Separator)) {
		return "", errors.New("target path escapes workspace directory via symlink")
	}
	if err := checkWorkspaceRelMediaRules(filepath.ToSlash(realRel), realTarget, allowAttachmentSubdir); err != nil {
		return "", err
	}
	return realTarget, nil
}

func writeMediaOutputFile(worktreeDir, dst string, data []byte) (string, error) {
	safeDst, err := resolveSafeWorkspaceMediaPath(worktreeDir, dst, true)
	if err != nil {
		return "", err
	}
	if st, statErr := os.Lstat(safeDst); statErr == nil && st.IsDir() {
		return "", errors.New("outputPath points to an existing directory")
	}
	if err := os.MkdirAll(filepath.Dir(safeDst), 0o755); err != nil {
		return "", err
	}
	if err := hoxipaths.WriteFileAtomic(safeDst, data, 0o644); err != nil {
		return "", err
	}
	realBase := filepath.Clean(worktreeDir)
	if evalBase, bErr := filepath.EvalSymlinks(realBase); bErr == nil && strings.TrimSpace(evalBase) != "" {
		realBase = filepath.Clean(evalBase)
	}
	if rel, relErr := filepath.Rel(realBase, safeDst); relErr == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel), nil
	}
	return safeDst, nil
}

func mediaExtForMime(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".png"
	}
}

func (g *Gateway) handleMediaImage(w http.ResponseWriter, r *http.Request) {
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
	var req mediaImageRequest
	if isAppMediaRequest(r) {
		if !decodeAppJSON(w, r, &req, 4<<20) {
			return
		}
	} else if err := json.NewDecoder(io.LimitReader(r.Body, 36<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}
	if isAppMediaRequest(r) && (req.Directory != "" || r.URL.Query().Has("directory") || req.OutputPath != "" || req.SessionID != "" || len(req.InputPaths) != 0 || len(req.InputDataURLs) != 0 || strings.TrimSpace(req.MaskDataURL) != "" || req.ReturnDataURL) {
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
	if len(req.InputDataURLs) > 4 {
		writeJSONError(w, http.StatusBadRequest, "at most 4 inline input images are allowed")
		return
	}
	for _, rawDataURL := range req.InputDataURLs {
		if strings.TrimSpace(rawDataURL) == "" {
			continue
		}
		parsedMime, decoded, decErr := attachment.DecodeDataURL(rawDataURL, "")
		if decErr != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid inputDataUrls item: "+decErr.Error())
			return
		}
		if !strings.HasPrefix(strings.ToLower(parsedMime), "image/") {
			writeJSONError(w, http.StatusBadRequest, "inputDataUrls must contain image/* data URLs")
			return
		}
		if len(decoded) > attachment.MaxAttachmentBytes {
			writeJSONError(w, http.StatusBadRequest, "inline input image exceeds 15 MB limit")
			return
		}
		inputImages = append(inputImages, llm.ImageInput{
			Mime: parsedMime,
			Data: decoded,
		})
	}
	var maskImage *llm.ImageInput
	if strings.TrimSpace(req.MaskDataURL) != "" {
		maskMime, maskBytes, maskErr := attachment.DecodeDataURL(req.MaskDataURL, "")
		if maskErr != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid maskDataUrl: "+maskErr.Error())
			return
		}
		if !strings.HasPrefix(strings.ToLower(maskMime), "image/") {
			writeJSONError(w, http.StatusBadRequest, "maskDataUrl must contain an image/* data URL")
			return
		}
		if len(maskBytes) > attachment.MaxAttachmentBytes {
			writeJSONError(w, http.StatusBadRequest, "mask image exceeds 15 MB limit")
			return
		}
		maskImage = &llm.ImageInput{Mime: maskMime, Data: maskBytes}
	}

	sessPID, sessMID := g.sessionActiveModel(req.SessionID)
	resolvedPID, resolvedMID, resolution, hasImgModel := g.providers.ResolveImageModelForSession(
		req.ProviderID, req.ModelID, sessPID, sessMID,
	)
	if !hasImgModel {
		if writeAppMediaFailure(w, r, llm.ErrUnavailable) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":                false,
			"missingCapability": "image",
			"fallbackTool":      "hoxi_tools",
			"error":             "neither the active model nor any configured local provider supports image generation; configure a Gemini Image, Imagen, GPT-Image, Flux, or Seedream model in Settings -> Providers, or fall back to hoxi_tools (discover -> inspect -> run) if cloud media APIs are available",
		})
		return
	}

	resp, err := g.providers.GenerateImage(r.Context(), llm.ImageRequest{
		ProviderID:  resolvedPID,
		ModelID:     resolvedMID,
		Prompt:      req.Prompt,
		AspectRatio: req.AspectRatio,
		ImageSize:   req.ImageSize,
		Count:       req.Count,
		InputImages: inputImages,
		MaskImage:   maskImage,
	})
	if err != nil {
		if writeAppMediaFailure(w, r, err) {
			return
		}
		if errors.Is(err, llm.ErrUnavailable) {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":                false,
				"missingCapability": "image",
				"fallbackTool":      "hoxi_tools",
				"error":             "neither the active model nor any configured local provider supports image generation; configure a Gemini Image, Imagen, GPT-Image, Flux, or Seedream model in Settings -> Providers, or fall back to hoxi_tools (discover -> inspect -> run) if cloud media APIs are available",
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	if isAppMediaRequest(r) {
		items := make([]appMediaOutput, 0, len(resp.Images))
		for _, item := range resp.Images {
			items = append(items, appMediaOutput{Mime: item.Mime, Data: item.Data})
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "providerID": resp.ProviderID,
			"modelID": resp.ModelID, "resolution": resolution, "images": items})
		return
	}

	nowMs := time.Now().UnixMilli()
	var attStore *attachment.Store
	if g.runner != nil {
		attStore = g.runner.Attachments()
	}

	var savedItems []mediaImageSavedItem
	for idx, img := range resp.Images {
		if len(img.Data) == 0 {
			continue
		}
		ext := mediaExtForMime(img.Mime)
		fname := fmt.Sprintf("generated_%d_%d%s", nowMs, idx+1, ext)
		item := mediaImageSavedItem{
			Filename: fname,
			Mime:     img.Mime,
		}
		if req.ReturnDataURL {
			itemMime := strings.TrimSpace(img.Mime)
			if itemMime == "" {
				itemMime = "image/png"
			}
			item.DataURL = "data:" + itemMime + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
		}
		if attStore != nil {
			if meta, saveErr := attStore.Save(attachment.SaveRequest{
				SessionID: req.SessionID,
				Directory: dir,
				Filename:  fname,
				Mime:      img.Mime,
				Data:      img.Data,
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
			relPath, wErr := writeMediaOutputFile(dir, dst, img.Data)
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
		"images":     savedItems,
	})
}

func (g *Gateway) handleMediaTranscribe(w http.ResponseWriter, r *http.Request) {
	if isRemoteRequest(r) {
		writeJSONError(w, http.StatusForbidden, "local audio transcription is not exposed over remote tunnel")
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

	var audioBytes []byte
	var filename, mime, language, prompt, providerID, modelID string

	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(maxLocalMediaUploadBytes + (1 << 20)); err != nil {
			writeJSONError(w, http.StatusBadRequest, "failed to parse multipart audio: "+err.Error())
			return
		}
		defer r.MultipartForm.RemoveAll()
		file, hdr, err := r.FormFile("file")
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "file field is required")
			return
		}
		defer file.Close()
		audioBytes, err = io.ReadAll(io.LimitReader(file, maxLocalMediaUploadBytes+1))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		filename = hdr.Filename
		mime = hdr.Header.Get("Content-Type")
		language = r.FormValue("language")
		prompt = r.FormValue("prompt")
		providerID = r.FormValue("providerID")
		modelID = r.FormValue("modelID")
	} else {
		var payload struct {
			DataURL    string `json:"dataUrl"`
			Filename   string `json:"filename"`
			Mime       string `json:"mime"`
			Language   string `json:"language"`
			Prompt     string `json:"prompt"`
			ProviderID string `json:"providerID"`
			ModelID    string `json:"modelID"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, maxLocalMediaUploadBytes*2)).Decode(&payload); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON payload")
			return
		}
		parsedMime, decoded, err := attachment.DecodeDataURL(payload.DataURL, payload.Mime)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid audio data URL: "+err.Error())
			return
		}
		audioBytes = decoded
		filename = payload.Filename
		mime = parsedMime
		language = payload.Language
		prompt = payload.Prompt
		providerID = payload.ProviderID
		modelID = payload.ModelID
	}

	res, err := g.providers.TranscribeAudio(r.Context(), llm.AudioTranscribeRequest{
		ProviderID: providerID,
		ModelID:    modelID,
		Filename:   filename,
		Mime:       mime,
		Data:       audioBytes,
		Language:   language,
		Prompt:     prompt,
	})
	if err != nil {
		if writeAppMediaFailure(w, r, err) {
			return
		}
		if errors.Is(err, llm.ErrUnavailable) {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":    false,
				"error": "no ASR or multimodal Gemini model is configured for voice transcription",
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"text":       res.Text,
		"providerID": res.ProviderID,
		"modelID":    res.ModelID,
	})
}
