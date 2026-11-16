package gateway

import (
	"encoding/json"
	"strings"

	"github.com/hoxiai/hoxi/internal/diagnostics"
)

// Called after runner's engine-event filter. The bounded SSE tap reassembles
// lines without changing the stream. Token events never perform disk I/O.
func inspectOpenCodeError(line string) {
	if !strings.HasPrefix(line, "data:") || !strings.Contains(line, "session.error") {
		return
	}
	type metadata struct {
		SessionID  string `json:"sessionID"`
		ProviderID string `json:"providerID"`
		ModelID    string `json:"modelID"`
	}
	type event struct {
		Type       string `json:"type"`
		Properties struct {
			metadata
			Error struct {
				Name string `json:"name"`
				Data struct {
					metadata
					Message    string `json:"message"`
					StatusCode int    `json:"statusCode"`
				} `json:"data"`
			} `json:"error"`
		} `json:"properties"`
	}
	var envelope struct {
		event
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &envelope) != nil {
		return
	}
	value := envelope.event
	if len(envelope.Payload) > 0 {
		if json.Unmarshal(envelope.Payload, &value) != nil {
			return
		}
	}
	if value.Type != "session.error" || value.Properties.SessionID == "" {
		return
	}
	p := value.Properties
	entry := diagnostics.SessionError(p.Error.Name, p.Error.Data.Message, p.Error.Data.StatusCode)
	if entry == nil {
		return
	}
	entry.Source, entry.SessionID = "opencode", p.SessionID
	entry.ProviderID, entry.ModelID = p.ProviderID, p.ModelID
	if entry.ProviderID == "" {
		entry.ProviderID = p.Error.Data.ProviderID
	}
	if entry.ModelID == "" {
		entry.ModelID = p.Error.Data.ModelID
	}
	diagnostics.Record(*entry)
}
