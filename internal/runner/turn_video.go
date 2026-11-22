package runner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/attachment"
	"github.com/hoxiai/hoxi/internal/llm"
)

// SetVideoClient wires the provider-backed video generator into the runner so
// chat sessions can execute video generation models natively.
func (s *Service) SetVideoClient(c llm.VideoClient) {
	if s != nil {
		s.videoClient = c
	}
}

// IsVideoModel reports whether providerID/modelID is handled by the native
// video generation turn pipeline.
func (s *Service) IsVideoModel(providerID, modelID string) bool {
	if s == nil || s.videoClient == nil {
		return false
	}
	return s.videoClient.IsVideoModel(strings.TrimSpace(providerID), strings.TrimSpace(modelID))
}

// IsDedicatedMediaModel reports whether providerID/modelID runs as a direct
// image or video generation turn rather than a conversational coding turn.
func (s *Service) IsDedicatedMediaModel(providerID, modelID string) bool {
	return s.IsImageModel(providerID, modelID) || s.IsVideoModel(providerID, modelID)
}

func videoAttachmentExtForMime(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "video/webm":
		return ".webm"
	case "video/quicktime":
		return ".mov"
	default:
		return ".mp4"
	}
}

func (s *Service) startVideoGenerationTurn(
	rec SessionRecord,
	mode, modelID string,
	userInfo MessageInfo,
	storedParts, deliveryParts []MessagePart,
	asstMsgID, promptText string,
	imgOpts *PromptImageOptions,
) error {
	if s.videoClient == nil {
		return fmt.Errorf("video generation client is not configured")
	}

	ctx, cancel := context.WithCancel(context.Background())
	ap := &activeProcess{
		sessionID: rec.ID,
		runtime:   "video",
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

		var aspectRatio string
		if imgOpts != nil {
			aspectRatio = strings.TrimSpace(imgOpts.AspectRatio)
		}
		resp, err := s.videoClient.GenerateVideo(ctx, llm.VideoRequest{
			ProviderID:  rec.ProviderID,
			ModelID:     modelID,
			Prompt:      effectivePrompt,
			AspectRatio: aspectRatio,
			InputImages: inputImages,
		})

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
			msgErr := newClassifiedMessageError("VideoGenerationError", err.Error(), 0)
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
		for idx, vid := range resp.Videos {
			if len(vid.Data) == 0 {
				continue
			}
			ext := videoAttachmentExtForMime(vid.Mime)
			filename := fmt.Sprintf("generated_video_%d_%d%s", endMs, idx+1, ext)
			fileURL := ""
			fileMime := strings.TrimSpace(vid.Mime)
			if fileMime == "" {
				fileMime = "video/mp4"
			}
			if s.attachments != nil {
				if meta, saveErr := s.attachments.Save(attachment.SaveRequest{
					SessionID: rec.ID,
					Directory: rec.WorktreePath,
					Filename:  filename,
					Mime:      fileMime,
					Data:      vid.Data,
					MaxBytes:  attachment.MaxVideoAttachmentBytes,
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
			vidPart := MessagePart{
				ID:        fmt.Sprintf("prt_a_vid_%d_%d", endMs, idx+1),
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
				Properties: map[string]interface{}{"part": vidPart},
			})
		}

		if savedCount == 0 && strings.TrimSpace(resp.Text) == "" {
			msgErr := newClassifiedMessageError("VideoGenerationError", "video model returned no video output", 0)
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
