package runner

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hoxiai/hoxi/internal/attachment"
)

func (s *Service) prepareTurnAttachments(
	rec SessionRecord,
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
	var cliImageNotes []string

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
			if meta.Kind == attachment.KindImage {
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
				if localPath != "" {
					cliImageNotes = append(cliImageNotes, fmt.Sprintf("[Attached Image: %s]", localPath))
				}
			} else {
				extracted := s.attachments.ExtractText(meta.ID, 48000)
				header := fmt.Sprintf(`<attached_file name=%q mime=%q`, meta.Filename, meta.Mime)
				if localPath != "" {
					header += fmt.Sprintf(` path=%q`, localPath)
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
			cliImageNotes = append(cliImageNotes, fmt.Sprintf("[Attached Image: %s]", saved[0]))
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
	if len(cliImageNotes) > 0 {
		joinedImgs := strings.Join(cliImageNotes, "\n")
		if cliPrompt == "" {
			cliPrompt = "Please analyze the attached image(s):\n" + joinedImgs
		} else {
			cliPrompt = cliPrompt + "\n\n" + joinedImgs
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
		return fmt.Sprintf("image_%d%s", index+1, ext)
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
