package runner

import "strings"

const (
	turnDirectiveOpen      = "<hoxi_turn_directive>"
	turnDirectiveClose     = "</hoxi_turn_directive>"
	nativeModelTextMetaKey = "nativeModelText"
)

// splitTurnDirective removes per-turn <hoxi_turn_directive> sections (such as
// the web client's media routing hints) from the caller system prompt. Keeping
// them out of the system prefix means a one-off directive does not invalidate
// the provider prompt cache for the whole conversation on that turn and again
// on the next; the directive travels with this turn's user input instead.
func splitTurnDirective(callerSystem string) (stable, directive string) {
	stable = strings.TrimSpace(callerSystem)
	if !strings.Contains(stable, turnDirectiveOpen) {
		return stable, ""
	}
	var parts []string
	rem := stable
	for {
		start := strings.Index(rem, turnDirectiveOpen)
		if start < 0 {
			break
		}
		rel := strings.Index(rem[start:], turnDirectiveClose)
		if rel < 0 {
			break
		}
		if inner := strings.TrimSpace(rem[start+len(turnDirectiveOpen) : start+rel]); inner != "" {
			parts = append(parts, inner)
		}
		rem = rem[start+rel+len(turnDirectiveClose):]
	}
	if len(parts) == 0 {
		return stripTaggedPromptSection(stable, turnDirectiveOpen, turnDirectiveClose), ""
	}
	return stripTaggedPromptSection(stable, turnDirectiveOpen, turnDirectiveClose), strings.Join(parts, "\n\n")
}

// withTurnDirective prefixes the model-facing prompt of a single turn with its
// directive. The stored, user-visible message parts are unaffected.
func withTurnDirective(directive, prompt string) string {
	if strings.TrimSpace(directive) == "" {
		return prompt
	}
	return turnDirectiveOpen + "\n" + directive + "\n" + turnDirectiveClose + "\n\n" + prompt
}

// attachNativeModelText stores the exact model-facing user text in hidden part
// metadata whenever it differs from the concise user-visible text (for example
// <hoxi_turn_directive>, inline skill instructions, slash-command expansion, or
// attachment annotations). Canonical history replay prefers this field so later
// turns stay byte-identical to the live turn and preserve upstream prompt caches.
func attachNativeModelText(storedParts []MessagePart, modelText string) {
	if strings.TrimSpace(modelText) == "" || len(storedParts) == 0 {
		return
	}
	targetIdx := -1
	for i := range storedParts {
		if storedParts[i].Type == "text" {
			targetIdx = i
			break
		}
	}
	if targetIdx >= 0 {
		if storedParts[targetIdx].Text == modelText {
			return
		}
	} else {
		targetIdx = 0
	}
	if storedParts[targetIdx].Metadata == nil {
		storedParts[targetIdx].Metadata = make(map[string]interface{})
	}
	storedParts[targetIdx].Metadata[nativeModelTextMetaKey] = modelText
}

func joinPromptSections(base, extra string) string {
	base, extra = strings.TrimSpace(base), strings.TrimSpace(extra)
	switch {
	case extra == "":
		return base
	case base == "":
		return extra
	default:
		return base + "\n\n" + extra
	}
}
