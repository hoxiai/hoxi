package turnstream

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

func ParseBase64DataURL(dataURL, fallbackMime string) (mimeType, b64Data string, rawBytes []byte, ok bool) {
	trimmed := strings.TrimSpace(dataURL)
	if !strings.HasPrefix(trimmed, "data:") {
		return "", "", nil, false
	}
	comma := strings.IndexByte(trimmed, ',')
	if comma < 0 {
		return "", "", nil, false
	}
	header := trimmed[5:comma]
	b64Data = strings.TrimSpace(trimmed[comma+1:])
	if b64Data == "" {
		return "", "", nil, false
	}
	mimeType = strings.TrimSpace(strings.Split(header, ";")[0])
	if mimeType == "" {
		mimeType = strings.TrimSpace(fallbackMime)
	}
	if mimeType == "" {
		mimeType = "image/png"
	}
	decoded, err := base64.StdEncoding.DecodeString(b64Data)
	if err != nil || len(decoded) == 0 {
		return "", "", nil, false
	}
	return mimeType, b64Data, decoded, true
}

func EncodeStreamInputTurn(runtime, promptText string) ([]byte, error) {
	return EncodeStreamInputTurnWithParts(runtime, promptText, nil)
}

func EncodeStreamInputTurnWithParts(runtime, promptText string, parts []MessagePart) ([]byte, error) {
	if runtime == string(RuntimeGeminiCLI) || runtime == string(RuntimeCodex) || runtime == string(RuntimeOpenCode) {
		return []byte(promptText + "\n"), nil
	}
	if runtime == string(RuntimeClaudeCode) && len(parts) > 0 {
		var contentBlocks []map[string]interface{}
		for _, p := range parts {
			if p.Type != "file" {
				continue
			}
			mimeType, b64Data, _, ok := ParseBase64DataURL(p.URL, p.Mime)
			if !ok {
				continue
			}
			contentBlocks = append(contentBlocks, map[string]interface{}{
				"type": "image",
				"source": map[string]interface{}{
					"type":       "base64",
					"media_type": mimeType,
					"data":       b64Data,
				},
			})
		}
		if len(contentBlocks) > 0 {
			contentBlocks = append(contentBlocks, map[string]interface{}{
				"type": "text",
				"text": promptText,
			})
			payload := map[string]interface{}{
				"type": "user",
				"message": map[string]interface{}{
					"role":    "user",
					"content": contentBlocks,
				},
			}
			b, err := json.Marshal(payload)
			return append(b, '\n'), err
		}
	}
	if IsClaudeProtocolRuntime(runtime) {
		payload := map[string]interface{}{
			"type": "user",
			"message": map[string]interface{}{
				"role":    "user",
				"content": promptText,
			},
		}
		b, err := json.Marshal(payload)
		return append(b, '\n'), err
	}
	payload := map[string]interface{}{
		"event": "user",
		"message": map[string]interface{}{
			"role":    "user",
			"content": promptText,
		},
	}
	b, err := json.Marshal(payload)
	return append(b, '\n'), err
}
