package runner

import (
	"encoding/json"

	"github.com/hoxiai/hoxi/internal/diagnostics"
)

func sessionErrorDiagnostic(sessionID string, failure interface{}) *diagnostics.Entry {
	var name, reason string
	var status int
	switch value := failure.(type) {
	case *MessageError:
		if value != nil {
			name = value.Name
			if value.Data != nil {
				status = value.Data.StatusCode
				reason = value.Data.Message
			}
		}
	case MessageError:
		name = value.Name
		if value.Data != nil {
			status = value.Data.StatusCode
			reason = value.Data.Message
		}
	case map[string]interface{}:
		name, _ = value["name"].(string)
		if data, ok := value["data"].(map[string]interface{}); ok {
			reason, _ = data["message"].(string)
			switch code := data["statusCode"].(type) {
			case int:
				status = code
			case float64:
				if code == float64(int(code)) {
					status = int(code)
				}
			case json.Number:
				n, err := code.Int64()
				if err == nil && n >= 100 && n <= 599 {
					status = int(n)
				}
			}
		}
	}
	entry := diagnostics.SessionError(name, reason, status)
	if entry != nil {
		entry.Source, entry.SessionID = "runner", sessionID
	}
	return entry
}
