package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrIncompleteStream indicates that an upstream SSE stream closed cleanly at
// the transport layer before emitting its protocol-level terminal frame
// (e.g. [DONE], finish_reason, response.completed, message_delta stop_reason,
// or candidate finishReason).
var ErrIncompleteStream = errors.New("unexpected EOF: model stream ended before terminal frame")

// IsIncompleteStreamError reports whether err was caused by a truncated model stream.
func IsIncompleteStreamError(err error) bool {
	return errors.Is(err, ErrIncompleteStream) || errors.Is(err, io.ErrUnexpectedEOF)
}

type sseRawFrame struct {
	Event string
	Data  string
}

func isCompleteSingleSSEPayload(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}
	if trimmed == "[DONE]" {
		return true
	}
	return (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && json.Valid([]byte(trimmed))
}

// readSSEFrames parses a Server-Sent Events stream, supporting standard
// blank-line-delimited multi-line `data:` blocks, `event:` headers, CRLF/LF
// endings, AND OpenAI-compatible relays that emit consecutive `data: {...}\n`
// lines without blank separator lines.
func readSSEFrames(ctx context.Context, body io.Reader, onFrame func(sseRawFrame) error) error {
	reader := bufio.NewReaderSize(body, 64*1024)
	var eventType string
	var dataLines []string

	flush := func() error {
		if len(dataLines) == 0 && eventType == "" {
			return nil
		}
		frame := sseRawFrame{
			Event: eventType,
			Data:  strings.Join(dataLines, "\n"),
		}
		eventType = ""
		dataLines = dataLines[:0]
		if frame.Data == "" && frame.Event == "" {
			return nil
		}
		return onFrame(frame)
	}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if line != "" {
			trimmed := strings.TrimRight(line, "\r\n")
			switch {
			case trimmed == "":
				if fErr := flush(); fErr != nil {
					return fErr
				}
			case strings.HasPrefix(trimmed, ":"):
				// SSE comment / keepalive frame; ignore.
			case strings.HasPrefix(trimmed, "event:"):
				if len(dataLines) > 0 {
					if fErr := flush(); fErr != nil {
						return fErr
					}
				}
				eventType = strings.TrimSpace(strings.TrimPrefix(trimmed, "event:"))
			case strings.HasPrefix(trimmed, "data:"):
				if len(dataLines) > 0 && isCompleteSingleSSEPayload(strings.Join(dataLines, "\n")) {
					if fErr := flush(); fErr != nil {
						return fErr
					}
				}
				val := strings.TrimPrefix(trimmed, "data:")
				if strings.HasPrefix(val, " ") {
					val = val[1:]
				}
				dataLines = append(dataLines, val)
			}
		}
		if errors.Is(err, io.EOF) {
			return flush()
		}
	}
}

// validateCompletedToolJSON verifies a finalized tool-call JSON argument string.
// When truncatedByLength is true (the model hit max_tokens mid-tool-call), an
// incomplete JSON payload is safely dropped (keep=false, err=nil) so sibling
// completed tool calls and partial text stay intact. Otherwise, malformed JSON
// fails explicitly as an incomplete stream rather than executing corrupt input.
func validateCompletedToolJSON(raw string, truncatedByLength bool) (cleaned json.RawMessage, keep bool, err error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return json.RawMessage("{}"), true, nil
	}
	if json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed), true, nil
	}
	if truncatedByLength {
		return nil, false, nil
	}
	return nil, false, fmt.Errorf("incomplete tool call JSON payload: %w", ErrIncompleteStream)
}

type inlineThinkSegment struct {
	thinking bool
	text     string
}

// inlineThinkFilter splits inline `<think>...</think>` blocks emitted inside
// `delta.content` by OpenAI-compatible relays that do not populate a dedicated
// `reasoning_content` field. Only `<think>` at the very start of the stream
// (ignoring leading whitespace) is treated as reasoning so literal `<think>`
// mentions inside code or prose later in the reply are never stripped.
type inlineThinkFilter struct {
	decided bool
	active  bool
	done    bool
	pending string
}

const (
	thinkOpenTag  = "<think>"
	thinkCloseTag = "</think>"
)

func (f *inlineThinkFilter) push(chunk string) []inlineThinkSegment {
	if chunk == "" {
		return nil
	}
	if f.done {
		return []inlineThinkSegment{{thinking: false, text: chunk}}
	}
	f.pending += chunk
	var out []inlineThinkSegment

	if !f.decided {
		trimmed := strings.TrimLeft(f.pending, " \t\r\n")
		if trimmed == "" {
			return nil
		}
		if strings.HasPrefix(thinkOpenTag, trimmed) && len(trimmed) < len(thinkOpenTag) {
			return nil
		}
		f.decided = true
		if strings.HasPrefix(trimmed, thinkOpenTag) {
			f.active = true
			f.pending = trimmed[len(thinkOpenTag):]
		} else {
			f.done = true
			txt := f.pending
			f.pending = ""
			return []inlineThinkSegment{{thinking: false, text: txt}}
		}
	}

	if f.active {
		if idx := strings.Index(f.pending, thinkCloseTag); idx >= 0 {
			before := f.pending[:idx]
			after := strings.TrimLeft(f.pending[idx+len(thinkCloseTag):], "\r\n")
			f.pending = ""
			f.active = false
			f.done = true
			if before != "" {
				out = append(out, inlineThinkSegment{thinking: true, text: before})
			}
			if after != "" {
				out = append(out, inlineThinkSegment{thinking: false, text: after})
			}
			return out
		}
		keepTail := longestTagPrefixSuffix(f.pending, thinkCloseTag)
		safeLen := len(f.pending) - keepTail
		if safeLen > 0 {
			emitTxt := f.pending[:safeLen]
			f.pending = f.pending[safeLen:]
			out = append(out, inlineThinkSegment{thinking: true, text: emitTxt})
		}
	}
	return out
}

func (f *inlineThinkFilter) flush() []inlineThinkSegment {
	if f.pending == "" {
		return nil
	}
	txt := f.pending
	f.pending = ""
	thinking := f.decided && f.active
	f.active = false
	f.done = true
	return []inlineThinkSegment{{thinking: thinking, text: txt}}
}

func longestTagPrefixSuffix(s, tag string) int {
	maxCheck := len(tag) - 1
	if len(s) < maxCheck {
		maxCheck = len(s)
	}
	for k := maxCheck; k > 0; k-- {
		if strings.HasSuffix(s, tag[:k]) {
			return k
		}
	}
	return 0
}
