package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/hoxiai/hoxi/internal/runner"
)

// StreamTurn subscribes to the unified SSE stream, dispatches the prompt, and
// invokes onEvent for each event belonging to session until the turn completes
// or ctx is cancelled.
func (c *Client) StreamTurn(
	ctx context.Context,
	session *runner.SessionInfo,
	prompt string,
	mode string,
	modelID string,
	providerID string,
	onEvent func(runner.SSEEvent),
) error {
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()

	sseURL := c.baseURL + "/api/hoxi/event"
	if strings.TrimSpace(session.Directory) != "" {
		sseURL += "?directory=" + url.QueryEscape(session.Directory)
	}
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, sseURL, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("X-Hoxi-Token", c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("SSE stream returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	provID := firstNonEmpty(providerID, session.ProviderID)
	modID := firstNonEmpty(modelID, session.ModelID)
	provID, modID = c.ResolveModel(ctx, session.Directory, session.Runtime, provID, modID)

	promptPayload := map[string]interface{}{
		"text":  prompt,
		"agent": firstNonEmpty(mode, "build"),
	}
	if modID != "" || provID != "" {
		promptPayload["model"] = map[string]string{
			"modelID":    modID,
			"providerID": provID,
		}
	}
	promptPath := "/api/hoxi/sessions/" + url.PathEscape(session.ID) + "/prompt"
	if session.Directory != "" {
		promptPath += "?directory=" + url.QueryEscape(session.Directory)
	}
	if err := c.doJSON(ctx, http.MethodPost, promptPath, promptPayload, nil); err != nil {
		return err
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	sawBusy := false
	askedPerms := make(map[string]bool)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		rawJSON := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if rawJSON == "" {
			continue
		}
		evt, ok := decodeSSEEvent(rawJSON)
		if !ok {
			continue
		}
		if evt.Type == "server.heartbeat" {
			continue
		}
		if evt.Type == "hoxi.stream.resync" {
			done, err := c.resyncTurn(ctx, session, askedPerms, onEvent)
			if err != nil {
				return err
			}
			if done {
				return nil
			}
			sawBusy = true
			continue
		}
		if sseEventSessionID(evt) != session.ID {
			continue
		}
		if evt.Type == "permission.asked" {
			if reqID, _ := evt.Properties["id"].(string); reqID != "" {
				if askedPerms[reqID] {
					continue
				}
				askedPerms[reqID] = true
			}
		}
		if onEvent != nil {
			onEvent(evt)
		}
		switch evt.Type {
		case "session.status":
			stMap, _ := evt.Properties["status"].(map[string]interface{})
			stType, _ := stMap["type"].(string)
			if stType == "busy" {
				sawBusy = true
			} else if stType == "idle" && sawBusy {
				return nil
			} else if stType == "idle" && !sawBusy {
				// Quick turns may emit message updates before the status transition.
				return nil
			}
		case "session.idle":
			return nil
		case "session.error":
			if errObj, ok := evt.Properties["error"].(map[string]interface{}); ok {
				if dataObj, ok := errObj["data"].(map[string]interface{}); ok {
					if msg, _ := dataObj["message"].(string); msg != "" {
						return fmt.Errorf("%s", msg)
					}
				}
				if name, _ := errObj["name"].(string); name != "" {
					return fmt.Errorf("%s", name)
				}
			}
			return fmt.Errorf("session encountered an error")
		}
	}
	return scanner.Err()
}

func decodeSSEEvent(rawJSON string) (runner.SSEEvent, bool) {
	var wrapper struct {
		runner.SSEEvent
		Payload *runner.SSEEvent `json:"payload"`
	}
	if json.Unmarshal([]byte(rawJSON), &wrapper) != nil {
		return runner.SSEEvent{}, false
	}
	if wrapper.Payload != nil && wrapper.Payload.Type != "" {
		return *wrapper.Payload, true
	}
	return wrapper.SSEEvent, wrapper.Type != ""
}

func sseEventSessionID(evt runner.SSEEvent) string {
	if sid, _ := evt.Properties["sessionID"].(string); strings.TrimSpace(sid) != "" {
		return strings.TrimSpace(sid)
	}
	switch info := evt.Properties["info"].(type) {
	case map[string]interface{}:
		if sid, _ := info["sessionID"].(string); strings.TrimSpace(sid) != "" {
			return strings.TrimSpace(sid)
		}
		if strings.HasPrefix(evt.Type, "session.") {
			if id, _ := info["id"].(string); strings.TrimSpace(id) != "" {
				return strings.TrimSpace(id)
			}
		}
	case runner.MessageInfo:
		return strings.TrimSpace(info.SessionID)
	case runner.SessionInfo:
		return strings.TrimSpace(info.ID)
	}
	switch part := evt.Properties["part"].(type) {
	case map[string]interface{}:
		if sid, _ := part["sessionID"].(string); strings.TrimSpace(sid) != "" {
			return strings.TrimSpace(sid)
		}
	case runner.MessagePart:
		return strings.TrimSpace(part.SessionID)
	}
	return ""
}

func (c *Client) resyncTurn(
	ctx context.Context,
	session *runner.SessionInfo,
	askedPerms map[string]bool,
	onEvent func(runner.SSEEvent),
) (done bool, turnErr error) {
	dirQuery := ""
	if strings.TrimSpace(session.Directory) != "" {
		dirQuery = "?directory=" + url.QueryEscape(session.Directory)
	}

	var turns []runner.MessageTurn
	msgPath := "/api/hoxi/sessions/" + url.PathEscape(session.ID) + "/message" + dirQuery
	if err := c.doJSON(ctx, http.MethodGet, msgPath, nil, &turns); err == nil {
		for _, turn := range currentTurnSlice(turns) {
			if onEvent != nil && turn.Info.ID != "" {
				if infoMap := toMap(turn.Info); infoMap != nil {
					onEvent(runner.SSEEvent{
						Type:       "message.updated",
						Properties: map[string]interface{}{"sessionID": session.ID, "info": infoMap},
					})
				}
			}
			for _, part := range turn.Parts {
				if onEvent != nil && part.ID != "" {
					if partMap := toMap(part); partMap != nil {
						onEvent(runner.SSEEvent{
							Type:       "message.part.updated",
							Properties: map[string]interface{}{"sessionID": session.ID, "part": partMap},
						})
					}
				}
			}
			if turn.Info.Role == "assistant" && turn.Info.Error != nil {
				msg := strings.TrimSpace(turn.Info.Error.Name)
				if turn.Info.Error.Data != nil && strings.TrimSpace(turn.Info.Error.Data.Message) != "" {
					msg = strings.TrimSpace(turn.Info.Error.Data.Message)
				}
				turnErr = fmt.Errorf("%s", firstNonEmpty(msg, "session encountered an error"))
			}
		}
	}

	var perms []map[string]interface{}
	if err := c.doJSON(ctx, http.MethodGet, "/api/hoxi/permission"+dirQuery, nil, &perms); err == nil {
		for _, perm := range perms {
			if sid, _ := perm["sessionID"].(string); strings.TrimSpace(sid) != session.ID {
				continue
			}
			if reqID, _ := perm["id"].(string); reqID != "" {
				if askedPerms[reqID] {
					continue
				}
				askedPerms[reqID] = true
			}
			if onEvent != nil {
				onEvent(runner.SSEEvent{Type: "permission.asked", Properties: perm})
			}
		}
	}
	if turnErr != nil {
		return true, turnErr
	}

	var statuses map[string]runner.SessionStatus
	if err := c.doJSON(ctx, http.MethodGet, "/api/hoxi/sessions/status"+dirQuery, nil, &statuses); err == nil {
		st, ok := statuses[session.ID]
		if !ok || st.Type == "" || st.Type == "idle" {
			return true, nil
		}
	}
	return false, nil
}

func currentTurnSlice(turns []runner.MessageTurn) []runner.MessageTurn {
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Info.Role == "user" {
			return turns[i:]
		}
	}
	return turns
}

func toMap(v interface{}) map[string]interface{} {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]interface{}
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}
