package runner

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/attachment"
)

func inferNativeModalityByModelID(providerID, modelID, modality string) bool {
	lowerMod := strings.ToLower(strings.TrimSpace(modelID))
	lowerProv := strings.ToLower(strings.TrimSpace(providerID))
	if lowerMod == "" {
		return false
	}
	for _, skip := range []string{"embed", "rerank", "whisper", "tts", "moderation", "imagen", "dall-e", "flux"} {
		if strings.Contains(lowerMod, skip) {
			return false
		}
	}
	isGemini := strings.Contains(lowerMod, "gemini") || strings.Contains(lowerProv, "google") || strings.Contains(lowerProv, "gemini")
	switch modality {
	case "pdf":
		if isGemini {
			return true
		}
		if strings.Contains(lowerMod, "claude-3-5") ||
			strings.Contains(lowerMod, "claude-3-7") ||
			strings.Contains(lowerMod, "claude-sonnet-4") ||
			strings.Contains(lowerMod, "claude-opus-4") ||
			strings.Contains(lowerMod, "claude-haiku-4") ||
			strings.Contains(lowerMod, "claude-4") {
			return true
		}
		if strings.HasPrefix(lowerMod, "gpt-4o") ||
			strings.HasPrefix(lowerMod, "chatgpt-4o") ||
			strings.HasPrefix(lowerMod, "gpt-4.1") ||
			strings.HasPrefix(lowerMod, "gpt-5") ||
			strings.HasPrefix(lowerMod, "o3") ||
			strings.HasPrefix(lowerMod, "o4") {
			return true
		}
		return false
	case "audio":
		if isGemini || strings.Contains(lowerMod, "omni") || strings.Contains(lowerMod, "audio") {
			return true
		}
		return false
	case "video":
		if isGemini || strings.Contains(lowerMod, "omni") || strings.Contains(lowerMod, "qwen2.5-vl") || strings.Contains(lowerMod, "qwen3-vl") {
			return true
		}
		return false
	default:
		return false
	}
}

func (s *Service) openCodeSupportsNativeAttachment(directory, providerID, modelID string, kind attachment.Kind) bool {
	var modality string
	switch kind {
	case attachment.KindPDF:
		modality = "pdf"
	case attachment.KindAudio:
		modality = "audio"
	case attachment.KindVideo:
		modality = "video"
	default:
		return false
	}

	prov := strings.TrimSpace(providerID)
	mod := strings.TrimSpace(modelID)
	if s != nil && s.openCode != nil && s.openCode.Available() && prov != "" && mod != "" {
		qCtx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		var resp struct {
			Providers []struct {
				ID     string                 `json:"id"`
				Models map[string]interface{} `json:"models"`
			} `json:"providers"`
		}
		err := s.openCodeJSON(qCtx, http.MethodGet, "/config/providers", directory, nil, &resp)
		cancel()
		if err == nil {
			for _, p := range resp.Providers {
				if p.ID != prov {
					continue
				}
				mObj, ok := p.Models[mod].(map[string]interface{})
				if !ok {
					break
				}
				if caps, ok := mObj["capabilities"].(map[string]interface{}); ok {
					if inMap, ok := caps["input"].(map[string]interface{}); ok {
						if flag, hasFlag := inMap[modality].(bool); hasFlag {
							return flag
						}
					}
				}
				if mods, ok := mObj["modalities"].(map[string]interface{}); ok {
					if rawIn, ok := mods["input"].([]interface{}); ok {
						for _, item := range rawIn {
							if sVal, ok := item.(string); ok && strings.EqualFold(strings.TrimSpace(sVal), modality) {
								return true
							}
						}
						return false
					}
				}
			}
		}
	}
	return inferNativeModalityByModelID(prov, mod, modality)
}

func (s *Service) prepareTurnAttachments(
	rec SessionRecord,
	now int64,
	seq int64,
	userMsgID string,
	promptText string,
	fileParts []MessagePart,
) (storedParts []MessagePart, deliveryParts []MessagePart, cliPrompt string) {
	return s.prepareTurnAttachmentsForModel(rec, rec.ProviderID, rec.ModelID, false, now, seq, userMsgID, promptText, fileParts)
}

func (s *Service) prepareTurnAttachmentsForModel(
	rec SessionRecord,
	targetProviderID string,
	targetModelID string,
	isImageTurn bool,
	now int64,
	seq int64,
	userMsgID string,
	promptText string,
	fileParts []MessagePart,
) (storedParts []MessagePart, deliveryParts []MessagePart, cliPrompt string) {
	textPartID := fmt.Sprintf("prt_u_%d_%d", now, seq)
	var storedFiles []MessagePart
	var deliveryFiles []MessagePart
	var docBlocks []string
	var cliMediaNotes []string

	for index, part := range fileParts {
		if part.Type != "file" || strings.TrimSpace(part.URL) == "" {
			continue
		}
		partID := fmt.Sprintf("prt_f_%d_%d_%d", now, seq, index)
		var meta attachment.Metadata
		var hasMeta bool

		if s.attachments != nil {
			if refID := attachment.ResolveRefID(part.URL); refID != "" {
				if m, _, err := s.attachments.Get(refID); err == nil {
					meta = m
					hasMeta = true
				}
			} else if strings.HasPrefix(strings.TrimSpace(part.URL), "data:") {
				if m, err := s.attachments.Save(attachment.SaveRequest{
					SessionID: rec.ID,
					Directory: rec.WorktreePath,
					Filename:  part.Filename,
					Mime:      part.Mime,
					DataURL:   part.URL,
				}); err == nil {
					meta = m
					hasMeta = true
				}
			}
		}

		if hasMeta {
			storedFiles = append(storedFiles, MessagePart{
				ID:        partID,
				SessionID: rec.ID,
				MessageID: userMsgID,
				Type:      "file",
				Mime:      meta.Mime,
				Filename:  meta.Filename,
				URL:       meta.URL,
			})
			var localPath string
			if rec.WorktreePath != "" {
				_, localPath, _ = s.attachments.MaterializeToDir(meta.ID, rec.WorktreePath)
			}

			nativeMultimodal := rec.Runtime == "opencode" &&
				!isImageTurn &&
				(meta.Kind == attachment.KindPDF || meta.Kind == attachment.KindAudio || meta.Kind == attachment.KindVideo) &&
				s.openCodeSupportsNativeAttachment(rec.WorktreePath, targetProviderID, targetModelID, meta.Kind)

			if meta.Kind == attachment.KindImage || nativeMultimodal {
				_, dataURL, err := s.attachments.ReadAsDataURL(meta.ID)
				if err != nil {
					dataURL = part.URL
				}
				deliveryFiles = append(deliveryFiles, MessagePart{
					ID:        partID,
					SessionID: rec.ID,
					MessageID: userMsgID,
					Type:      "file",
					Mime:      meta.Mime,
					Filename:  meta.Filename,
					URL:       dataURL,
				})
				if meta.Kind == attachment.KindImage {
					if localPath != "" {
						cliMediaNotes = append(cliMediaNotes, fmt.Sprintf("[Attached Image: %s]", localPath))
					}
				} else if localPath != "" {
					header := fmt.Sprintf(`<attached_file name=%q mime=%q path=%q native_multimodal="true"></attached_file>`, meta.Filename, meta.Mime, localPath)
					docBlocks = append(docBlocks, header)
				}
			} else if meta.Kind == attachment.KindAudio || meta.Kind == attachment.KindVideo {
				label := "Audio"
				if meta.Kind == attachment.KindVideo {
					label = "Video"
				}
				if localPath != "" {
					cliMediaNotes = append(cliMediaNotes, fmt.Sprintf("[Attached %s: %s]", label, localPath))
					docBlocks = append(docBlocks, fmt.Sprintf(`<attached_file name=%q mime=%q path=%q></attached_file>`, meta.Filename, meta.Mime, localPath))
				} else {
					docBlocks = append(docBlocks, fmt.Sprintf(`<attached_file name=%q mime=%q></attached_file>`, meta.Filename, meta.Mime))
				}
			} else {
				extracted := s.attachments.ExtractText(meta.ID, 48000)
				header := fmt.Sprintf(`<attached_file name=%q mime=%q`, meta.Filename, meta.Mime)
				if localPath != "" {
					header += fmt.Sprintf(` path=%q cli_hint="hoxi doc inspect|read|set-cell|write-xlsx|write-docx|write-pptx|write-pdf"`, localPath)
				}
				header += ">"
				if extracted != "" {
					docBlocks = append(docBlocks, header+"\n"+extracted+"\n</attached_file>")
				} else {
					docBlocks = append(docBlocks, header+"\n</attached_file>")
				}
			}
			continue
		}

		// Fallback if attachment store is unavailable or URL is external
		storedPart := part
		storedPart.ID = partID
		storedPart.SessionID, storedPart.MessageID = rec.ID, userMsgID
		storedFiles = append(storedFiles, storedPart)
		deliveryFiles = append(deliveryFiles, storedPart)
		if saved := materializeTurnAttachments(rec.WorktreePath, now, seq, []MessagePart{part}); len(saved) > 0 {
			cliMediaNotes = append(cliMediaNotes, fmt.Sprintf("[Attached File: %s]", saved[0]))
		}
	}

	effectiveText := promptText
	if len(docBlocks) > 0 {
		joinedDocs := strings.Join(docBlocks, "\n\n")
		if effectiveText == "" {
			effectiveText = "Please analyze the attached file(s):\n\n" + joinedDocs
		} else {
			effectiveText = effectiveText + "\n\n" + joinedDocs
		}
	}

	cliPrompt = effectiveText
	if len(cliMediaNotes) > 0 {
		joinedMedia := strings.Join(cliMediaNotes, "\n")
		if cliPrompt == "" {
			cliPrompt = "Please analyze the attached file(s):\n" + joinedMedia
		} else {
			cliPrompt = cliPrompt + "\n\n" + joinedMedia
		}
	}

	if promptText != "" || len(storedFiles) == 0 {
		storedParts = append(storedParts, MessagePart{
			ID:        textPartID,
			SessionID: rec.ID,
			MessageID: userMsgID,
			Type:      "text",
			Text:      promptText,
		})
	}
	storedParts = append(storedParts, storedFiles...)

	if effectiveText != "" || len(deliveryFiles) == 0 {
		deliveryParts = append(deliveryParts, MessagePart{
			ID:        textPartID,
			SessionID: rec.ID,
			MessageID: userMsgID,
			Type:      "text",
			Text:      effectiveText,
		})
	}
	deliveryParts = append(deliveryParts, deliveryFiles...)

	return storedParts, deliveryParts, cliPrompt
}

func parseBase64DataURL(dataURL, fallbackMime string) (mimeType, b64Data string, rawBytes []byte, ok bool) {
	trimmed := strings.TrimSpace(dataURL)
	if !strings.HasPrefix(trimmed, "data:") {
		return "", "", nil, false
	}
	comma := strings.IndexByte(trimmed, ',')
	if comma < 0 {
		return "", "", nil, false
	}
	header := trimmed[5:comma]
	b64Data = strings.TrimSpace(trimmed[comma+1:])
	if b64Data == "" {
		return "", "", nil, false
	}
	mimeType = strings.TrimSpace(strings.Split(header, ";")[0])
	if mimeType == "" {
		mimeType = strings.TrimSpace(fallbackMime)
	}
	if mimeType == "" {
		mimeType = "image/png"
	}
	decoded, err := base64.StdEncoding.DecodeString(b64Data)
	if err != nil || len(decoded) == 0 {
		return "", "", nil, false
	}
	return mimeType, b64Data, decoded, true
}

func attachmentExtForMime(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "application/pdf":
		return ".pdf"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/wav":
		return ".wav"
	case "audio/mp4":
		return ".m4a"
	case "audio/ogg":
		return ".ogg"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "video/quicktime":
		return ".mov"
	default:
		return ".png"
	}
}

func sanitizeAttachmentFilename(name, mimeType string, index int) string {
	base := filepath.Base(strings.TrimSpace(name))
	var clean strings.Builder
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			clean.WriteRune(r)
		}
	}
	s := strings.Trim(clean.String(), "._-")
	ext := attachmentExtForMime(mimeType)
	if s == "" {
		return fmt.Sprintf("attachment_%d%s", index+1, ext)
	}
	if filepath.Ext(s) == "" {
		s += ext
	}
	return s
}

func materializeTurnAttachments(worktreePath string, now int64, seq int64, parts []MessagePart) []string {
	wt := strings.TrimSpace(worktreePath)
	if wt == "" || len(parts) == 0 {
		return nil
	}
	attachDir := filepath.Join(wt, ".hoxi", "attachments")
	if err := os.MkdirAll(attachDir, 0o700); err != nil {
		return nil
	}
	var saved []string
	for idx, p := range parts {
		if p.Type != "file" {
			continue
		}
		mimeType, _, raw, ok := parseBase64DataURL(p.URL, p.Mime)
		if !ok {
			continue
		}
		safeName := sanitizeAttachmentFilename(p.Filename, mimeType, idx)
		target := filepath.Join(attachDir, fmt.Sprintf("%d_%d_%d_%s", now, seq, idx+1, safeName))
		if err := os.WriteFile(target, raw, 0o600); err == nil {
			saved = append(saved, target)
		}
	}
	return saved
}

func (s *Service) MaterializeAttachmentToDir(id, targetDir string) (string, error) {
	if s == nil || s.attachments == nil {
		return "", fmt.Errorf("attachment store unavailable")
	}
	_, dst, err := s.attachments.MaterializeToDir(id, targetDir)
	return dst, err
}
