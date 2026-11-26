package agent

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// decodeMCPToolResult preserves the full bounded server result in metadata while
// projecting text/structured resources and inline media into model context.
// Resource links are descriptions only: following one requires a separate tool
// call and its normal approval, never an implicit network or local file read.
func decodeMCPToolResult(title string, raw json.RawMessage) (ToolOutput, error) {
	out := ToolOutput{Title: title}
	var result struct {
		IsError           bool              `json:"isError"`
		Content           []json.RawMessage `json:"content"`
		StructuredContent json.RawMessage   `json:"structuredContent"`
	}
	fail := func(err error) (ToolOutput, error) {
		out.IsError = true
		out.Output = err.Error()
		return out, err
	}
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return fail(fmt.Errorf("MCP tool returned an empty result"))
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fail(fmt.Errorf("invalid MCP tool result: %w", err))
	}
	out.Metadata = map[string]any{"mcpResult": append(json.RawMessage(nil), raw...)}
	var parts []string
	if len(result.StructuredContent) > 0 && string(result.StructuredContent) != "null" {
		parts = append(parts, "Structured result:\n"+string(result.StructuredContent))
	}
	addMedia := func(mime, data, name string) error {
		if strings.TrimSpace(mime) == "" || strings.TrimSpace(data) == "" {
			return fmt.Errorf("MCP media is missing mimeType or data")
		}
		if len(data) > base64.StdEncoding.EncodedLen(maxReadMediaBytes) {
			return fmt.Errorf("MCP media exceeds inline size limit")
		}
		if _, err := base64.StdEncoding.DecodeString(data); err != nil {
			return fmt.Errorf("MCP media contains invalid base64: %w", err)
		}
		out.Media = append(out.Media, ContentBlock{
			Type: ContentMedia, MimeType: mime, Data: data, Filename: name,
		})
		parts = append(parts, fmt.Sprintf("[MCP attachment: %s (%s)]", name, mime))
		return nil
	}
	for i, rawContent := range result.Content {
		var c struct {
			Type     string          `json:"type"`
			Text     string          `json:"text"`
			MimeType string          `json:"mimeType"`
			Data     string          `json:"data"`
			Name     string          `json:"name"`
			Resource json.RawMessage `json:"resource"`
		}
		if err := json.Unmarshal(rawContent, &c); err != nil {
			return fail(fmt.Errorf("invalid MCP content %d: %w", i+1, err))
		}
		switch c.Type {
		case "text":
			// MCP servers SHOULD mirror structuredContent as serialized JSON text;
			// projecting both would bill the same payload twice.
			if mcpTextMirrorsStructured(c.Text, result.StructuredContent) {
				continue
			}
			parts = append(parts, c.Text)
		case "image", "audio":
			name := c.Name
			if name == "" {
				name = fmt.Sprintf("mcp-%s-%d", c.Type, i+1)
			}
			if err := addMedia(c.MimeType, c.Data, name); err != nil {
				return fail(err)
			}
		case "resource":
			var resource struct {
				URI      string  `json:"uri"`
				MimeType string  `json:"mimeType"`
				Text     *string `json:"text"`
				Blob     *string `json:"blob"`
			}
			if err := json.Unmarshal(c.Resource, &resource); err != nil {
				return fail(fmt.Errorf("invalid MCP embedded resource: %w", err))
			}
			if resource.Text != nil {
				parts = append(parts, fmt.Sprintf("[Resource: %s (%s)]\n%s", resource.URI, resource.MimeType, *resource.Text))
			}
			if resource.Blob != nil {
				if err := addMedia(resource.MimeType, *resource.Blob, resource.URI); err != nil {
					return fail(err)
				}
			}
			if resource.Text == nil && resource.Blob == nil {
				return fail(fmt.Errorf("MCP embedded resource has neither text nor blob"))
			}
		case "resource_link":
			parts = append(parts, "[Resource link; not fetched]\n"+string(rawContent))
		default:
			// Preserve future content types instead of silently dropping them.
			parts = append(parts, "[Unrecognized MCP content]\n"+string(rawContent))
		}
	}
	out.Output = strings.Join(parts, "\n")
	out.IsError = result.IsError
	if result.IsError {
		if strings.TrimSpace(out.Output) == "" {
			out.Output = "MCP tool returned an error without explanatory content"
		}
		return out, fmt.Errorf("MCP tool returned error")
	}
	if len(parts) == 0 {
		out.Output = string(raw)
	}
	return out, nil
}

// mcpTextMirrorsStructured reports whether text is a JSON serialization of the
// same value as structured. Non-JSON or differing text is always kept.
func mcpTextMirrorsStructured(text string, structured json.RawMessage) bool {
	if len(structured) == 0 || string(structured) == "null" {
		return false
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
		return false
	}
	var fromText, fromStructured any
	if json.Unmarshal([]byte(trimmed), &fromText) != nil || json.Unmarshal(structured, &fromStructured) != nil {
		return false
	}
	return reflect.DeepEqual(fromText, fromStructured)
}
