package runner

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hoxiai/hoxi/internal/attachment"
	"github.com/hoxiai/hoxi/internal/llm"
)

// SetImageClient wires the provider-backed image generator into the runner so
// chat sessions can execute image generation models natively without OpenCode's
// coding agent tool declarations.
func (s *Service) SetImageClient(c llm.ImageClient) {
	if s != nil {
		s.imageClient = c
	}
}

// IsImageModel reports whether providerID/modelID is handled by the native
// image generation turn pipeline.
func (s *Service) IsImageModel(providerID, modelID string) bool {
	if s == nil || s.imageClient == nil {
		return false
	}
	return s.imageClient.IsImageModel(strings.TrimSpace(providerID), strings.TrimSpace(modelID))
}

func (s *Service) startImageGenerationTurn(
	rec SessionRecord,
	mode, modelID string,
	userInfo MessageInfo,
	storedParts, deliveryParts []MessagePart,
	asstMsgID, promptText string,
	imgOpts *PromptImageOptions,
) error {
	if s.imageClient == nil {
		return fmt.Errorf("image generation client is not configured")
	}

	ctx, cancel := context.WithCancel(context.Background())
	ap := &activeProcess{
		sessionID: rec.ID,
		runtime:   "image",
		done:      make(chan struct{}),
		abortCh:   make(chan struct{}),
		cancel:    cancel,
	}
	ap.stall.touch(time.Now())

	s.mu.Lock()
	s.procs[rec.ID] = ap
	s.mu.Unlock()

	if s.status != nil {
		s.status.SetSessionBusy(rec.ID, true)
	}

	startMs := time.Now().UnixMilli()
	if startMs <= userInfo.Time.Created {
		startMs = userInfo.Time.Created + 1
	}
	asstInfo := MessageInfo{
		ID:         asstMsgID,
		SessionID:  rec.ID,
		Role:       "assistant",
		ParentID:   userInfo.ID,
		Time:       MessageTime{Created: startMs},
		ModelID:    modelID,
		ProviderID: rec.ProviderID,
		Mode:       mode,
		Agent:      mode,
	}

	s.broadcast(rec.ID,
		SSEEvent{
			Type: "session.status",
			Properties: map[string]interface{}{
				"sessionID": rec.ID,
				"status":    SessionStatus{Type: "busy"},
			},
		},
		SSEEvent{
			Type:       "message.updated",
			Properties: map[string]interface{}{"info": userInfo},
		},
	)
	for _, sp := range storedParts {
		s.broadcast(rec.ID, SSEEvent{
			Type:       "message.part.updated",
			Properties: map[string]interface{}{"part": sp},
		})
	}
	s.broadcast(rec.ID, SSEEvent{
		Type:       "message.updated",
		Properties: map[string]interface{}{"info": asstInfo},
	})

	inputImages := s.extractTurnInputImages(deliveryParts)
	usedHistoryImage := false
	if len(inputImages) == 0 {
		if histImg, ok := s.latestGeneratedImageFromHistory(rec); ok {
			inputImages = []llm.ImageInput{histImg}
			usedHistoryImage = true
		}
	}

	effectivePrompt := strings.TrimSpace(promptText)
	if effectivePrompt == "" {
		for _, dp := range deliveryParts {
			if dp.Type == "text" && strings.TrimSpace(dp.Text) != "" {
				effectivePrompt = strings.TrimSpace(dp.Text)
				break
			}
		}
	}

	go func() {
		var errEvt *SSEEvent
		defer func() {
			cancel()
			if !ap.wasRestarting() {
				s.clearInflightTurnForUserMsg(rec.ID, userInfo.ID)
			}
			s.syncSessionMemoryFile(rec.ID)
			s.store.FlushSession(rec.ID)
			s.releaseActiveProcess(rec.ID, ap)
			if !ap.wasRestarting() {
				if errEvt != nil && !ap.wasSteered() {
					s.broadcast(rec.ID, *errEvt)
				}
				s.broadcast(rec.ID, SSEEvent{
					Type: "session.status",
					Properties: map[string]interface{}{
						"sessionID": rec.ID,
						"status":    SessionStatus{Type: "idle"},
					},
				})
			}
			close(ap.done)
		}()

		var aspectRatio, imageSize string
		var imgCount int
		if imgOpts != nil {
			aspectRatio = strings.TrimSpace(imgOpts.AspectRatio)
			imageSize = strings.TrimSpace(imgOpts.ImageSize)
			imgCount = imgOpts.Count
		}
		resp, err := s.imageClient.GenerateImage(ctx, llm.ImageRequest{
			ProviderID:  rec.ProviderID,
			ModelID:     modelID,
			Prompt:      effectivePrompt,
			AspectRatio: aspectRatio,
			ImageSize:   imageSize,
			Count:       imgCount,
			InputImages: inputImages,
		})
		if err != nil && usedHistoryImage && !ap.wasAborted() {
			// Some pure text-to-image endpoints reject reference images; retry
			// cleanly with text-only prompt when the image came from turn history.
			resp, err = s.imageClient.GenerateImage(ctx, llm.ImageRequest{
				ProviderID:  rec.ProviderID,
				ModelID:     modelID,
				Prompt:      effectivePrompt,
				AspectRatio: aspectRatio,
				ImageSize:   imageSize,
				Count:       imgCount,
			})
		}

		endMs := time.Now().UnixMilli()
		if endMs < startMs {
			endMs = startMs
		}

		if ap.wasAborted() {
			asstInfo.Time.Completed = endMs
			asstInfo.Finish = "aborted"
			s.broadcast(rec.ID, SSEEvent{
				Type:       "message.updated",
				Properties: map[string]interface{}{"info": asstInfo},
			})
			return
		}

		if err != nil {
			msgErr := newClassifiedMessageError("ImageGenerationError", err.Error(), 0)
			asstInfo.Time.Completed = endMs
			asstInfo.Error = msgErr
			asstInfo.Finish = "error"
			s.broadcast(rec.ID, SSEEvent{
				Type:       "message.updated",
				Properties: map[string]interface{}{"info": asstInfo},
			})
			errEvt = &SSEEvent{
				Type: "session.error",
				Properties: map[string]interface{}{
					"sessionID": rec.ID,
					"error":     msgErr,
				},
			}
			return
		}

		if trimmedText := strings.TrimSpace(resp.Text); trimmedText != "" {
			textPart := MessagePart{
				ID:        fmt.Sprintf("prt_a_txt_%d", endMs),
				SessionID: rec.ID,
				MessageID: asstInfo.ID,
				Type:      "text",
				Text:      trimmedText,
				Time:      &PartTime{Start: startMs, End: endMs},
			}
			s.broadcast(rec.ID, SSEEvent{
				Type:       "message.part.updated",
				Properties: map[string]interface{}{"part": textPart},
			})
		}

		savedCount := 0
		for idx, img := range resp.Images {
			if len(img.Data) == 0 {
				continue
			}
			ext := attachmentExtForMime(img.Mime)
			filename := fmt.Sprintf("generated_%d_%d%s", endMs, idx+1, ext)
			fileURL := ""
			fileMime := strings.TrimSpace(img.Mime)
			if fileMime == "" {
				fileMime = "image/png"
			}
			if s.attachments != nil {
				if meta, saveErr := s.attachments.Save(attachment.SaveRequest{
					SessionID: rec.ID,
					Directory: rec.WorktreePath,
					Filename:  filename,
					Mime:      fileMime,
					Data:      img.Data,
				}); saveErr == nil {
					fileURL = meta.URL
					fileMime = meta.Mime
					filename = meta.Filename
					if strings.TrimSpace(rec.WorktreePath) != "" {
						_, _, _ = s.attachments.MaterializeToDir(meta.ID, rec.WorktreePath)
					}
				}
			}
			if fileURL == "" {
				continue
			}
			savedCount++
			imgPart := MessagePart{
				ID:        fmt.Sprintf("prt_a_img_%d_%d", endMs, idx+1),
				SessionID: rec.ID,
				MessageID: asstInfo.ID,
				Type:      "file",
				Mime:      fileMime,
				Filename:  filename,
				URL:       fileURL,
				Time:      &PartTime{Start: startMs, End: endMs},
			}
			s.broadcast(rec.ID, SSEEvent{
				Type:       "message.part.updated",
				Properties: map[string]interface{}{"part": imgPart},
			})
		}

		if savedCount == 0 && strings.TrimSpace(resp.Text) == "" {
			msgErr := newClassifiedMessageError("ImageGenerationError", "image model returned no image output", 0)
			asstInfo.Time.Completed = endMs
			asstInfo.Error = msgErr
			asstInfo.Finish = "error"
			s.broadcast(rec.ID, SSEEvent{
				Type:       "message.updated",
				Properties: map[string]interface{}{"info": asstInfo},
			})
			errEvt = &SSEEvent{
				Type: "session.error",
				Properties: map[string]interface{}{
					"sessionID": rec.ID,
					"error":     msgErr,
				},
			}
			return
		}

		if resp.InputTokens > 0 || resp.OutputTokens > 0 {
			asstInfo.Tokens = &MessageTokens{
				Total:  resp.InputTokens + resp.OutputTokens,
				Input:  resp.InputTokens,
				Output: resp.OutputTokens,
			}
		}
		asstInfo.Time.Completed = endMs
		asstInfo.Finish = "stop"
		s.broadcast(rec.ID, SSEEvent{
			Type:       "message.updated",
			Properties: map[string]interface{}{"info": asstInfo},
		})

		s.maybeAutoTitleImageSession(rec.ID, effectivePrompt)
	}()

	return nil
}

func (s *Service) extractTurnInputImages(deliveryParts []MessagePart) []llm.ImageInput {
	var out []llm.ImageInput
	for _, p := range deliveryParts {
		if p.Type != "file" || strings.TrimSpace(p.URL) == "" {
			continue
		}
		if mimeType, _, raw, ok := parseBase64DataURL(p.URL, p.Mime); ok && len(raw) > 0 {
			out = append(out, llm.ImageInput{Mime: mimeType, Data: raw})
			continue
		}
		if s.attachments != nil {
			if refID := attachment.ResolveRefID(p.URL); refID != "" {
				if meta, raw, err := s.attachments.ReadBytes(refID); err == nil && meta.Kind == attachment.KindImage && len(raw) > 0 {
					out = append(out, llm.ImageInput{Mime: meta.Mime, Data: raw})
				}
			}
		}
	}
	return out
}

func (s *Service) latestGeneratedImageFromHistory(rec SessionRecord) (llm.ImageInput, bool) {
	if s.attachments == nil || len(rec.Turns) == 0 {
		return llm.ImageInput{}, false
	}
	for i := len(rec.Turns) - 1; i >= 0; i-- {
		turn := rec.Turns[i]
		if turn.Info.Role != "assistant" {
			continue
		}
		for j := len(turn.Parts) - 1; j >= 0; j-- {
			p := turn.Parts[j]
			if p.Type != "file" {
				continue
			}
			refID := attachment.ResolveRefID(p.URL)
			if refID == "" {
				continue
			}
			meta, raw, err := s.attachments.ReadBytes(refID)
			if err == nil && meta.Kind == attachment.KindImage && len(raw) > 0 {
				return llm.ImageInput{Mime: meta.Mime, Data: raw}, true
			}
		}
		// Check only the most recent assistant turn so stale images from old turns do not leak in.
		break
	}
	return llm.ImageInput{}, false
}

func (s *Service) maybeAutoTitleImageSession(sessionID, promptText string) {
	clean := strings.Join(strings.Fields(strings.TrimSpace(promptText)), " ")
	if clean == "" {
		return
	}
	if utf8.RuneCountInString(clean) > 36 {
		runes := []rune(clean)
		clean = string(runes[:36]) + "…"
	}
	updated, ok, err := s.store.Update(sessionID, func(r *SessionRecord) {
		if r.Title == "" || r.Title == "New Session" || strings.HasPrefix(r.Title, "New session - ") {
			r.Title = clean
		}
	})
	if err == nil && ok && updated.Title == clean {
		s.broadcast(sessionID, SSEEvent{
			Type:       "session.updated",
			Properties: map[string]interface{}{"info": updated.ToSessionInfo()},
		})
	}
}
