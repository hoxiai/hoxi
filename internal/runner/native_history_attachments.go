package runner

import (
	"fmt"
	"strings"

	"github.com/hoxiai/hoxi/internal/attachment"
)

func (s *Service) canonicalUserBlocks(t MessageTurn, hasTarget bool, supportsAttachment func(attachment.Kind) bool) []NativeContentBlock {
	var blocks []NativeContentBlock
	var nativeModelText, userText string
	for _, p := range t.Parts {
		if userText == "" && p.Type == "text" {
			userText = p.Text
		}
		if nativeModelText == "" {
			if text, _ := p.Metadata[nativeModelTextMetaKey].(string); strings.TrimSpace(text) != "" {
				nativeModelText = text
			}
		}
	}
	if nativeModelText != "" {
		blocks = append(blocks, NativeContentBlock{Type: "text", Text: nativeModelText})
	}
	userTextEnd := 0
	if at := strings.Index(nativeModelText, userText); at >= 0 {
		userTextEnd = at + len(userText)
	}
	for _, p := range t.Parts {
		switch p.Type {
		case "text":
			if nativeModelText != "" {
				continue
			}
			if strings.TrimSpace(p.Text) != "" {
				blocks = append(blocks, NativeContentBlock{Type: "text", Text: p.Text})
			}
		case "file":
			rawURL := strings.TrimSpace(p.URL)
			if strings.HasPrefix(rawURL, "/api/hoxi/file/content?") {
				if nativeModelText != "" {
					continue
				}
				localPath, _ := resolveStagedWorkspaceFilePath(rawURL, "")
				hdr := `<attached_file name="` + p.Filename + `" mime="` + p.Mime + `"`
				if localPath != "" {
					hdr += ` path="` + localPath + `"`
				}
				if by := stringMetaField(p.Metadata, "transcribedBy"); by != "" {
					hdr += ` transcribed_by="` + by + `"`
				}
				if tr := stringMetaField(p.Metadata, "transcript"); tr != "" {
					blocks = append(blocks, NativeContentBlock{
						Type: "text",
						Text: hdr + ">\n[Audio Transcript]\n" + tr + "\n</attached_file>",
					})
				} else {
					blocks = append(blocks, NativeContentBlock{
						Type: "text",
						Text: hdr + "></attached_file>",
					})
				}
				continue
			}
			lowerMime := strings.ToLower(strings.TrimSpace(p.Mime))
			audio := strings.HasPrefix(lowerMime, "audio/")
			video := strings.HasPrefix(lowerMime, "video/")
			if (audio && !supportsAttachment(attachment.KindAudio)) || (video && !supportsAttachment(attachment.KindVideo)) {
				if tr := stringMetaField(p.Metadata, "transcript"); tr != "" {
					if nativeModelText != "" && strings.Contains(nativeModelText[userTextEnd:], "[Audio Transcript]\n"+tr+"\n</attached_file>") {
						continue
					}
					hdr := `<attached_file name="` + p.Filename + `" mime="` + p.Mime + `"`
					if by := stringMetaField(p.Metadata, "transcribedBy"); by != "" {
						hdr += ` transcribed_by="` + by + `"`
					}
					blocks = append(blocks, NativeContentBlock{
						Type: "text",
						Text: hdr + ">\n[Audio Transcript]\n" + tr + "\n</attached_file>",
					})
					continue
				}
				if hasTarget || nativeModelText != "" {
					continue
				}
			}
			if lowerMime == "application/pdf" && (nativeModelText != "" || hasTarget) && !supportsAttachment(attachment.KindPDF) {
				if extract := s.missingPDFExtract(nativeModelText, userTextEnd, p); extract != "" {
					blocks = append(blocks, NativeContentBlock{Type: "text", Text: extract})
				}
				continue
			}
			if nativeModelText != "" && lowerMime != "" &&
				!strings.HasPrefix(lowerMime, "image/") && lowerMime != "application/pdf" &&
				!audio && !video {
				continue
			}
			if mb, ok := s.resolveNativeHistoryFileBlock(p); ok {
				if lowerMime == "application/pdf" && nativeModelText != "" {
					text, useMedia := s.textForNativePDF(blocks[0].Text, userTextEnd, p)
					if !useMedia {
						continue
					}
					blocks[0].Text = text
				}
				if tr := stringMetaField(p.Metadata, "transcript"); nativeModelText != "" && audio && tr != "" {
					text := blocks[0].Text
					body := recordedAttachmentBody(text, userTextEnd, p, attachment.ResolveRefID(p.URL), "transcribed_by")
					transcript := "[Audio Transcript]\n" + tr
					if strings.HasPrefix(body, transcript+"\n</attached_file>") {
						start := len(text) - len(body)
						blocks[0].Text = text[:start] + text[start+len(transcript):]
					}
				}
				blocks = append(blocks, mb)
			}
		}
	}
	return blocks
}

// Keep the user's words and the attachment path when upgrading an extracted PDF
// to a native file. Older sessions have no recorded length, so verify their extract
// against the immutable attachment before removing it.
func (s *Service) textForNativePDF(text string, userTextEnd int, p MessagePart) (string, bool) {
	const closeTag = "\n</attached_file>"
	if size := intMetaField(p.Metadata, "pdfExtractBytes"); size > 0 {
		tail := intMetaField(p.Metadata, "pdfExtractTrailingBytes")
		if tail < len(closeTag) || tail > len(text) || size >= len(text)-tail {
			return text, false
		}
		end := len(text) - tail
		start := end - size
		if start < userTextEnd || text[start-1] != '\n' || !strings.HasPrefix(text[end:], closeTag) {
			return text, false
		}
		return text[:start] + text[end:], true
	}
	id := attachment.ResolveRefID(p.URL)
	if id == "" || s.attachments == nil {
		return text, true
	}
	body := recordedAttachmentBody(text, userTextEnd, p, id, "cli_hint")
	if body == "" || strings.HasPrefix(body, "</attached_file>") {
		return text, true
	}
	extracted := s.attachments.ExtractText(id, 48000)
	if extracted == "" || !strings.HasPrefix(body, extracted+closeTag) {
		return text, false
	}
	start := len(text) - len(body)
	return text[:start] + text[start+len(extracted):], true
}

func recordedAttachmentBody(text string, userTextEnd int, p MessagePart, id, attribute string) string {
	open := fmt.Sprintf("\n\n<attached_file name=%q mime=%q", p.Filename, p.Mime)
	for end := len(text); end > 0; {
		i := strings.LastIndex(text[:end], open)
		if i < userTextEnd {
			break
		}
		end = i
		header, body, ok := strings.Cut(text[i+len(open):], "\n")
		if !ok {
			continue
		}
		if header != ">" {
			if !strings.Contains(header, " "+attribute+"=") {
				continue
			}
			if strings.HasPrefix(header, " path=") && (id == "" || !strings.Contains(header, id+"_")) {
				continue
			}
		}
		return body
	}
	return ""
}

// Supply a text-only target with the same bounded local extract used on upload.
// Read only the immutable attachment and preserve an extract already in history.
func (s *Service) missingPDFExtract(text string, userTextEnd int, p MessagePart) string {
	id := attachment.ResolveRefID(p.URL)
	if id == "" || s.attachments == nil || intMetaField(p.Metadata, "pdfExtractBytes") > 0 {
		return ""
	}
	if body := recordedAttachmentBody(text, userTextEnd, p, id, "cli_hint"); body != "" {
		return ""
	}
	extract := s.attachments.ExtractText(id, 48000)
	if extract == "" {
		return ""
	}
	return fmt.Sprintf("<attached_file name=%q mime=%q>\n%s\n</attached_file>", p.Filename, p.Mime, extract)
}
