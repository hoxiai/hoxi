package runner

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/attachment"
	"github.com/hoxiai/hoxi/internal/llm"
)

// SetAudioClient wires the provider-backed audio transcriber into the runner so
// uploaded audio attachments can be automatically transcribed into text when
// the active chat/coding model or CLI runtime does not accept native audio input.
func (s *Service) SetAudioClient(c llm.AudioClient) {
	if s != nil {
		s.audioClient = c
	}
}

// SetStreamClient wires the provider stream target resolver so the native Go
// agent only passes raw audio/video inlineData when the wire protocol is native
// Google Gemini, routing OpenAI/Anthropic protocol audio through ASR instead.
func (s *Service) SetStreamClient(c llm.StreamClient) {
	if s != nil {
		s.streamClient = c
	}
}

func (s *Service) isNativeGoogleStreamProtocol(providerID, modelID string) bool {
	if s != nil && s.streamClient != nil {
		return s.streamClient.ResolveStreamTarget(providerID, modelID).Protocol == llm.ProtocolGoogle
	}
	lp := strings.ToLower(strings.TrimSpace(providerID))
	return lp == "google" || lp == "gemini" || strings.HasPrefix(lp, "google-")
}

func (s *Service) runtimeSupportsNativeAttachment(rec SessionRecord, providerID, modelID string, kind attachment.Kind) bool {
	if kind != attachment.KindPDF && kind != attachment.KindAudio && kind != attachment.KindVideo {
		return false
	}
	switch rec.Runtime {
	case string(RuntimeNative):
		if (kind == attachment.KindAudio || kind == attachment.KindVideo) && !s.isNativeGoogleStreamProtocol(providerID, modelID) {
			return false
		}
		return s.openCodeSupportsNativeAttachment(rec.WorktreePath, providerID, modelID, kind)
	case "opencode":
		return s.openCodeSupportsNativeAttachment(rec.WorktreePath, providerID, modelID, kind)
	default:
		return false
	}
}

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
	pdfExtractFiles := make(map[int]int)
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

			nativeMultimodal := !isImageTurn &&
				s.runtimeSupportsNativeAttachment(rec, targetProviderID, targetModelID, meta.Kind)

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
				var transcript string
				var transcribedBy string
				if meta.Kind == attachment.KindAudio && s.audioClient != nil && s.audioClient.HasAudioTranscriber() {
					if _, rawAudio, rErr := s.attachments.ReadBytes(meta.ID); rErr == nil && len(rawAudio) > 0 {
						tCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
						tRes, tErr := s.audioClient.TranscribeAudio(tCtx, llm.AudioTranscribeRequest{
							Filename: meta.Filename,
							Mime:     meta.Mime,
							Data:     rawAudio,
						})
						cancel()
						if tErr == nil && strings.TrimSpace(tRes.Text) != "" {
							transcript = strings.TrimSpace(tRes.Text)
							transcribedBy = strings.Trim(tRes.ProviderID+"/"+tRes.ModelID, "/")
						}
					}
				}
				if localPath != "" {
					cliMediaNotes = append(cliMediaNotes, fmt.Sprintf("[Attached %s: %s]", label, localPath))
				}
				header := fmt.Sprintf(`<attached_file name=%q mime=%q`, meta.Filename, meta.Mime)
				if localPath != "" {
					header += fmt.Sprintf(` path=%q`, localPath)
				}
				if transcribedBy != "" {
					header += fmt.Sprintf(` transcribed_by=%q`, transcribedBy)
				}
				header += ">"
				if transcript != "" {
					if idx := len(storedFiles) - 1; idx >= 0 {
						storedFiles[idx].Metadata = map[string]interface{}{
							"transcript":    transcript,
							"transcribedBy": transcribedBy,
						}
					}
					docBlocks = append(docBlocks, header+"\n[Audio Transcript]\n"+transcript+"\n</attached_file>")
				} else {
					docBlocks = append(docBlocks, header+"</attached_file>")
				}
			} else {
				extracted := s.attachments.ExtractText(meta.ID, 48000)
				header := fmt.Sprintf(`<attached_file name=%q mime=%q`, meta.Filename, meta.Mime)
				if localPath != "" {
					header += fmt.Sprintf(` path=%q cli_hint="hoxi doc inspect|read|set-cell|write-xlsx|write-docx|write-pptx|write-pdf"`, localPath)
				}
				header += ">"
				if extracted != "" {
					if rec.Runtime == string(RuntimeNative) && meta.Kind == attachment.KindPDF {
						pdfExtractFiles[len(docBlocks)] = len(storedFiles) - 1
						storedFiles[len(storedFiles)-1].Metadata = map[string]interface{}{"pdfExtractBytes": len(extracted)}
					}
					docBlocks = append(docBlocks, header+"\n"+extracted+"\n</attached_file>")
				} else {
					docBlocks = append(docBlocks, header+"\n</attached_file>")
				}
			}
			continue
		}

		// Staged workspace media/files served via /api/hoxi/file/content?path=...&raw=1.
		// Keep the playable file part in storedFiles for the timeline UI; pass the
		// workspace path to the model, and inline native multimodal media only when
		// the target model supports it and the file is within the 20 MiB inline cap.
		if rawURL := strings.TrimSpace(part.URL); strings.HasPrefix(rawURL, "/api/hoxi/file/content?") {
			storedPart := part
			storedPart.ID = partID
			storedPart.SessionID, storedPart.MessageID = rec.ID, userMsgID
			storedFiles = append(storedFiles, storedPart)

			localPath, relPath := resolveStagedWorkspaceFilePath(rawURL, rec.WorktreePath)
			fname := strings.TrimSpace(part.Filename)
			if fname == "" && relPath != "" {
				fname = filepath.Base(relPath)
			}
			mimeType := strings.TrimSpace(part.Mime)
			if mimeType == "" && localPath != "" {
				mimeType = attachment.DetectFileMimeType(localPath, nil)
			}
			lowerMime := strings.ToLower(mimeType)
			ext := strings.ToLower(filepath.Ext(fname))
			var kind attachment.Kind
			label := "File"
			switch {
			case strings.HasPrefix(lowerMime, "video/") || ext == ".mp4" || ext == ".mov" || ext == ".webm" || ext == ".mkv" || ext == ".avi" || ext == ".m4v":
				kind = attachment.KindVideo
				label = "Video"
			case strings.HasPrefix(lowerMime, "audio/") || ext == ".mp3" || ext == ".wav" || ext == ".m4a" || ext == ".aac" || ext == ".ogg" || ext == ".oga" || ext == ".opus" || ext == ".flac":
				kind = attachment.KindAudio
				label = "Audio"
			case strings.HasPrefix(lowerMime, "image/") || ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp" || ext == ".gif":
				kind = attachment.KindImage
				label = "Image"
			case lowerMime == "application/pdf" || ext == ".pdf":
				kind = attachment.KindPDF
				label = "PDF"
			}
			if localPath != "" {
				cliMediaNotes = append(cliMediaNotes, fmt.Sprintf("[Attached %s: %s]", label, localPath))
			}

			nativeInlined := false
			if !isImageTurn && localPath != "" && (kind == attachment.KindImage || s.runtimeSupportsNativeAttachment(rec, targetProviderID, targetModelID, kind)) {
				if raw := readStagedMedia(rec.WorktreePath, localPath, maxStagedNativeInlineBytes); len(raw) > 0 {
					if mimeType == "" {
						mimeType = attachment.DetectFileMimeType(localPath, raw)
					}
					file := MessagePart{ID: partID, SessionID: rec.ID, MessageID: userMsgID, Type: "file", Mime: mimeType, Filename: fname}
					file.URL = "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(raw)
					if rec.Runtime == string(RuntimeNative) {
						storedFiles[len(storedFiles)-1], file = s.snapshotStagedMedia(rec, file, raw)
					}
					deliveryFiles = append(deliveryFiles, file)
					nativeInlined = true
				}
			}

			var transcript string
			var transcribedBy string
			if !nativeInlined && kind == attachment.KindAudio && localPath != "" && s.audioClient != nil && s.audioClient.HasAudioTranscriber() {
				const maxStagedAudioTranscribeBytes = 25 << 20
				if rawAudio := readStagedMedia(rec.WorktreePath, localPath, maxStagedAudioTranscribeBytes); len(rawAudio) > 0 {
					tCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
					tRes, tErr := s.audioClient.TranscribeAudio(tCtx, llm.AudioTranscribeRequest{
						Filename: fname,
						Mime:     mimeType,
						Data:     rawAudio,
					})
					cancel()
					if tErr == nil && strings.TrimSpace(tRes.Text) != "" {
						transcript = strings.TrimSpace(tRes.Text)
						transcribedBy = strings.Trim(tRes.ProviderID+"/"+tRes.ModelID, "/")
					}
				}
			}
			header := fmt.Sprintf(`<attached_file name=%q mime=%q`, fname, mimeType)
			if localPath != "" {
				header += fmt.Sprintf(` path=%q`, localPath)
			}
			if nativeInlined {
				header += ` native_multimodal="true"`
			}
			if transcribedBy != "" {
				header += fmt.Sprintf(` transcribed_by=%q`, transcribedBy)
			}
			header += ">"
			if transcript != "" {
				if idx := len(storedFiles) - 1; idx >= 0 {
					storedFiles[idx].Metadata = map[string]interface{}{
						"transcript":    transcript,
						"transcribedBy": transcribedBy,
					}
				}
				docBlocks = append(docBlocks, header+"\n[Audio Transcript]\n"+transcript+"\n</attached_file>")
			} else {
				docBlocks = append(docBlocks, header+"</attached_file>")
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

	// Directive and skill expansion prepend text. Distances from the end remain
	// valid after expansion and when earlier PDF extracts are removed in file order.
	if len(pdfExtractFiles) > 0 {
		trailing := len(cliPrompt) - len(effectiveText)
		for i := len(docBlocks) - 1; i >= 0; i-- {
			if fileIdx, ok := pdfExtractFiles[i]; ok {
				storedFiles[fileIdx].Metadata["pdfExtractTrailingBytes"] = trailing + len("\n</attached_file>")
			}
			trailing += len(docBlocks[i]) + len("\n\n")
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

const maxStagedNativeInlineBytes = 20 << 20

// Use the same immutable bytes on the live request and on all later turns. The
// inline URL is also a usable snapshot when the attachment store is unavailable.
func (s *Service) snapshotStagedMedia(rec SessionRecord, file MessagePart, raw []byte) (stored, delivery MessagePart) {
	if s.attachments == nil {
		return file, file
	}
	meta, err := s.attachments.Save(attachment.SaveRequest{
		SessionID: rec.ID, Directory: rec.WorktreePath, Filename: file.Filename, Mime: file.Mime, Data: raw, MaxBytes: maxStagedNativeInlineBytes,
	})
	if err != nil {
		return file, file
	}
	if meta.Mime != file.Mime {
		_, normalized, err := s.attachments.ReadAsDataURL(meta.ID)
		if err != nil {
			return file, file
		}
		file.URL, file.Mime, file.Filename = normalized, meta.Mime, meta.Filename
	}
	stored = file
	stored.URL = meta.URL
	return stored, file
}

// Read through a rooted handle so symlinks cannot escape the session workspace.
func readStagedMedia(worktreeDir, localPath string, limit int64) []byte {
	rel, err := filepath.Rel(worktreeDir, localPath)
	if err != nil {
		return nil
	}
	root, err := os.OpenRoot(worktreeDir)
	if err != nil {
		return nil
	}
	defer root.Close()
	info, err := root.Stat(rel)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return nil
	}
	file, err := root.Open(rel)
	if err != nil {
		return nil
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil
	}
	return data
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

func resolveStagedWorkspaceFilePath(rawURL, fallbackWorktree string) (localPath, relPath string) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", ""
	}
	relPath = strings.TrimSpace(parsed.Query().Get("path"))
	dirParam := strings.TrimSpace(parsed.Query().Get("directory"))
	if relPath == "" {
		return "", ""
	}
	if filepath.IsAbs(relPath) {
		return filepath.Clean(relPath), relPath
	}
	if dirParam != "" {
		return filepath.Join(filepath.Clean(dirParam), filepath.FromSlash(relPath)), relPath
	}
	if strings.TrimSpace(fallbackWorktree) != "" {
		return filepath.Join(filepath.Clean(fallbackWorktree), filepath.FromSlash(relPath)), relPath
	}
	return relPath, relPath
}

func (s *Service) MaterializeAttachmentToDir(id, targetDir string) (string, error) {
	if s == nil || s.attachments == nil {
		return "", fmt.Errorf("attachment store unavailable")
	}
	_, dst, err := s.attachments.MaterializeToDir(id, targetDir)
	return dst, err
}
