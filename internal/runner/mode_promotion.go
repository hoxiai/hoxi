package runner

import (
	"errors"
	"strings"

	"github.com/hoxiai/hoxi/internal/llm"
)

func (s *Service) applyNativeModePromotion(sessionID, nextMode string, tr *TurnTranslator) {
	nextMode = strings.TrimSpace(nextMode)
	if s == nil || sessionID == "" || nextMode == "" {
		return
	}
	if updated, ok, err := s.store.UpdateMetadata(sessionID, func(meta *SessionMetadata) {
		meta.Mode = nextMode
	}); err == nil && ok {
		s.broadcast(sessionID, SSEEvent{
			Type:       "session.updated",
			Properties: map[string]interface{}{"info": updated.ToSessionInfo()},
		})
	}
	if tr != nil {
		tr.MsgInfo.Mode = nextMode
		tr.MsgInfo.Agent = nextMode
		s.broadcastRouted(sessionID, []SSEEvent{{
			Type:       "message.updated",
			Properties: map[string]interface{}{"info": tr.MsgInfo},
		}})
	}
}

func classifyNativeAgentFailureError(err error, hasSideEffect bool) *MessageError {
	statusCode := 0
	var streamHTTPErr *llm.StreamHTTPError
	if errors.As(err, &streamHTTPErr) && streamHTTPErr != nil {
		statusCode = streamHTTPErr.StatusCode
	}
	classified := newClassifiedMessageError("NativeAgentError", boundEngineText(err.Error()), statusCode)
	if classified != nil && classified.Data != nil {
		if streamHTTPErr != nil {
			if streamHTTPErr.RetryAfter > 0 && classified.Data.RetryAfterMs <= 0 {
				classified.Data.RetryAfterMs = streamHTTPErr.RetryAfter.Milliseconds()
			}
			if streamHTTPErr.Upstream != nil {
				upCopy := *streamHTTPErr.Upstream
				if upCopy.Headers != nil {
					upCopy.Headers = cloneStringMap(upCopy.Headers)
				}
				classified.Data.Upstream = &upCopy
			}
		}
		if hasSideEffect {
			classified.Data.IsRetryable = false
			classified.Data.Kind = ToolOutcomeUnknown
		}
	}
	return classified
}
