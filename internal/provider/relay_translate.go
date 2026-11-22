package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func (s *Service) serveTranslatedRelayChat(
	w http.ResponseWriter,
	r *http.Request,
	providerID, modelID, baseURL, authSecret string,
	headers map[string]string,
	proto string,
	payload map[string]any,
	scoped bool,
) {
	stream, _ := payload["stream"].(bool)
	started := time.Now()
	elapsedMs := func() int64 {
		ms := time.Since(started).Milliseconds()
		if ms <= 0 {
			return 1
		}
		return ms
	}

	var replyText string
	var promptToks, completionToks int
	var status int
	var rawErr []byte
	var err error

	if proto == "google" {
		replyText, promptToks, completionToks, status, rawErr, err = s.callRelayGoogleChat(
			r.Context(), baseURL, authSecret, headers, modelID, payload,
		)
	} else {
		replyText, promptToks, completionToks, status, rawErr, err = s.callRelayAnthropicChat(
			r.Context(), baseURL, authSecret, headers, modelID, payload,
		)
	}

	if err != nil {
		s.recordBreakerFailure(providerID, modelID, classifyCandidateFailure(0, err, nil), 0, elapsedMs(), err.Error())
		recordRelayFailure(providerID, modelID, 0, started)
		if scoped {
			writeScopedRelayError(w)
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]any{"message": "upstream request failed: " + err.Error(), "type": "upstream_error"},
		})
		return
	}
	if status < 200 || status >= 300 {
		s.recordBreakerFailure(providerID, modelID, classifyCandidateFailure(status, nil, rawErr), status, elapsedMs(), string(rawErr))
		recordRelayFailure(providerID, modelID, status, started)
		if scoped {
			writeScopedRelayError(w)
			return
		}
		writeRelayUpstreamError(w, status, rawErr)
		return
	}

	s.recordBreakerSuccess(providerID, modelID, elapsedMs())
	nowSec := time.Now().Unix()
	completionID := fmt.Sprintf("chatcmpl-hoxi-%d", time.Now().UnixNano())

	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		chunk := map[string]any{
			"id":      completionID,
			"object":  "chat.completion.chunk",
			"created": nowSec,
			"model":   providerID + "/" + modelID,
			"choices": []map[string]any{{
				"index":         0,
				"delta":         map[string]any{"role": "assistant", "content": replyText},
				"finish_reason": nil,
			}},
		}
		stopChunk := map[string]any{
			"id":      completionID,
			"object":  "chat.completion.chunk",
			"created": nowSec,
			"model":   providerID + "/" + modelID,
			"choices": []map[string]any{{
				"index":         0,
				"delta":         map[string]any{},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{
				"prompt_tokens":     promptToks,
				"completion_tokens": completionToks,
				"total_tokens":      promptToks + completionToks,
			},
		}
		b1, _ := json.Marshal(chunk)
		b2, _ := json.Marshal(stopChunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: %s\n\ndata: [DONE]\n\n", b1, b2)
		_ = http.NewResponseController(w).Flush()
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":      completionID,
		"object":  "chat.completion",
		"created": nowSec,
		"model":   providerID + "/" + modelID,
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": replyText,
			},
			"finish_reason": "stop",
		}},
		"usage": map[string]int{
			"prompt_tokens":     promptToks,
			"completion_tokens": completionToks,
			"total_tokens":      promptToks + completionToks,
		},
	})
}
