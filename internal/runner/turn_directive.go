package runner

import "strings"

const (
	turnDirectiveOpen  = "<hoxi_turn_directive>"
	turnDirectiveClose = "</hoxi_turn_directive>"
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
