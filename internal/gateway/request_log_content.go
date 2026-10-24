package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/runner"
)

// requestLogContentMaxChars bounds each text part so an enormous reply cannot
// blow up the response. The tail is dropped with a marker instead.
const (
	requestLogContentMaxChars = 20000
	requestLogContentMaxBytes = 4 << 20
)

// requestContentPart is one visible chunk of a request: the user's input text
// or the model's text/reasoning output. Roles and kinds let the UI label them.
type requestContentPart struct {
	Role string `json:"role"`
	Kind string `json:"kind"` // "text" | "reasoning"
	Text string `json:"text"`
}

type requestContent struct {
	Input     []requestContentPart `json:"input"`
	Output    []requestContentPart `json:"output"`
	Found     bool                 `json:"found"`
	Truncated bool                 `json:"truncated,omitempty"`
}

// handleRequestLogContent returns the user message that triggered a logged call
// and the model's reply for it. This is local conversation content read from the
// user's own store; the route is absent from the tunnel allow-list.
func (g *Gateway) handleRequestLogContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	messageID := strings.TrimSpace(r.URL.Query().Get("id"))
	if messageID == "" {
		writeJSONError(w, http.StatusBadRequest, "id is required")
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("sessionID"))
	writeJSON(w, http.StatusOK, g.requestLogContent(sessionID, messageID))
}

func (g *Gateway) requestLogContent(sessionID, messageID string) requestContent {
	if content, ok := g.runnerRequestContent(sessionID, messageID); ok {
		return content
	}
	return openCodeRequestContent(sessionID, messageID)
}

func (g *Gateway) runnerRequestContent(sessionID, messageID string) (requestContent, bool) {
	if g.runner == nil || strings.TrimSpace(sessionID) == "" {
		return requestContent{}, false
	}
	store := g.runner.Store()
	if _, ok := store.Meta(sessionID); !ok {
		return requestContent{}, false
	}
	var content requestContent
	found := false
	store.VisitTurns(sessionID, func(full runner.SessionRecord) {
		index := -1
		for i := range full.Turns {
			if full.Turns[i].Info.ID == messageID {
				index = i
				break
			}
		}
		if index < 0 {
			return
		}
		found = true
		content.Output, content.Truncated = turnTextParts(full.Turns[index], "assistant")
		for i := index - 1; i >= 0; i-- {
			if full.Turns[i].Info.Role == "user" {
				input, truncated := turnTextParts(full.Turns[i], "user")
				content.Input = input
				content.Truncated = content.Truncated || truncated
				break
			}
		}
	})
	if !found {
		return requestContent{}, false
	}
	content.Found = true
	return content, true
}

func turnTextParts(turn runner.MessageTurn, role string) ([]requestContentPart, bool) {
	var parts []requestContentPart
	truncated := false
	for i := range turn.Parts {
		part := turn.Parts[i]
		if part.Type != "text" && part.Type != "reasoning" {
			continue
		}
		if strings.TrimSpace(part.Text) == "" {
			continue
		}
		text, cut := clipText(part.Text)
		truncated = truncated || cut
		parts = append(parts, requestContentPart{Role: role, Kind: part.Type, Text: text})
	}
	return parts, truncated
}

func openCodeRequestContent(sessionID, messageID string) requestContent {
	dbPath := hoxipaths.DBPath()
	if dbPath == "" {
		return requestContent{}
	}
	if _, err := os.Stat(dbPath); err != nil {
		return requestContent{}
	}
	msgLit := sqlLiteral(messageID)
	sesLit := sqlLiteral(sessionID)
	sqlQuery := fmt.Sprintf(`SELECT json_object(
  'found', (SELECT COUNT(*) FROM message WHERE id = %s),
  'input', COALESCE((
    SELECT json_group_array(json_object('data', d.data)) FROM (
      SELECT p.data AS data FROM part p WHERE p.message_id = (
        SELECT m.id FROM message m
        WHERE m.session_id = COALESCE(NULLIF(%s, ''), (SELECT m2.session_id FROM message m2 WHERE m2.id = %s))
          AND json_extract(m.data, '$.role') = 'user'
          AND m.time_created <= (SELECT time_created FROM message WHERE id = %s)
        ORDER BY m.time_created DESC, m.id DESC LIMIT 1
      ) AND json_extract(p.data, '$.type') IN ('text', 'reasoning')
      ORDER BY p.time_created, p.id
    ) d
  ), json('[]')),
  'output', COALESCE((
    SELECT json_group_array(json_object('data', d.data)) FROM (
      SELECT p.data AS data FROM part p
      WHERE p.message_id = %s
        AND json_extract(p.data, '$.type') IN ('text', 'reasoning')
      ORDER BY p.time_created, p.id
    ) d
  ), json('[]'))
);`, msgLit, sesLit, msgLit, msgLit, msgLit)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sqlite3", "-readonly", "-cmd", ".timeout 5000", dbPath, sqlQuery)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return requestContent{}
	}
	if err := cmd.Start(); err != nil {
		return requestContent{}
	}
	out, readErr := io.ReadAll(io.LimitReader(stdout, requestLogContentMaxBytes))
	_ = cmd.Wait()
	if readErr != nil || len(bytes.TrimSpace(out)) == 0 {
		return requestContent{}
	}
	var payload struct {
		Found  int        `json:"found"`
		Input  []partData `json:"input"`
		Output []partData `json:"output"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &payload); err != nil || payload.Found == 0 {
		return requestContent{}
	}
	content := requestContent{Found: true}
	content.Input, content.Truncated = decodeContentParts(payload.Input, "user")
	output, truncated := decodeContentParts(payload.Output, "assistant")
	content.Output = output
	content.Truncated = content.Truncated || truncated
	return content
}

type partData struct {
	Data string `json:"data"`
}

func decodeContentParts(items []partData, role string) ([]requestContentPart, bool) {
	var parts []requestContentPart
	truncated := false
	for _, item := range items {
		var raw struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(item.Data), &raw) != nil {
			continue
		}
		if raw.Type != "text" && raw.Type != "reasoning" {
			continue
		}
		if strings.TrimSpace(raw.Text) == "" {
			continue
		}
		text, cut := clipText(raw.Text)
		truncated = truncated || cut
		parts = append(parts, requestContentPart{Role: role, Kind: raw.Type, Text: text})
	}
	return parts, truncated
}

// clipText trims trailing blank lines and caps the rune count.
func clipText(v string) (string, bool) {
	v = strings.TrimRight(v, "\n")
	if utf8.RuneCountInString(v) <= requestLogContentMaxChars {
		return v, false
	}
	runes := []rune(v)
	// Slice on runes so a multi-byte character is never cut in half.
	return string(runes[:requestLogContentMaxChars]) + "…", true
}

// sqlLiteral quotes a value for the sqlite3 CLI. SQLite string literals have no
// backslash escapes, so doubling single quotes is sufficient.
func sqlLiteral(v string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(v, "\x00", ""), "'", "''") + "'"
}
