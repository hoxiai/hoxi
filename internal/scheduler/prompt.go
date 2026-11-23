package scheduler

import (
	"fmt"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/diagnostics"
)

const (
	verdictOKPrefix      = "[[schedule:ok]]"
	verdictAlertPrefix   = "[[schedule:alert]]"
	verdictBlockedPrefix = "[[schedule:blocked]]"
)

// BuildTurnSystem constructs the hidden system context injected into each
// scheduled turn (timestamp, workspace identity, optional previous summary,
// and structured verdict contract).
func BuildTurnSystem(sch Schedule, triggerKind string, now time.Time) string {
	var b strings.Builder
	b.WriteString("You are executing a Hoxi scheduled task.\n")
	fmt.Fprintf(&b, "- Schedule: %s (%s)\n", sch.Name, sch.ID)
	fmt.Fprintf(&b, "- Trigger: %s at %s\n", triggerKind, now.Format(time.RFC3339))
	if sch.Directory != "" {
		fmt.Fprintf(&b, "- Workspace directory: %s\n", sch.Directory)
	}
	b.WriteString("- Do not create, modify, or delete scheduled tasks during a scheduled run.\n")
	if sch.InjectLastSummary && strings.TrimSpace(sch.SessionID) == "" && strings.TrimSpace(sch.LastSummary) != "" {
		b.WriteString("\nPrevious scheduled run summary (use for delta comparison and deduplicating alerts):\n")
		if sch.LastOutcome != "" {
			fmt.Fprintf(&b, "Last status: %s\n", sch.LastOutcome)
		}
		b.WriteString(truncateRunes(sch.LastSummary, MaxSummaryRunes))
		b.WriteString("\n")
	}
	b.WriteString("\nOutcome reporting:\n")
	b.WriteString("End your final reply with one single status line using one of these exact markers:\n")
	b.WriteString("- [[schedule:ok]] <one-sentence summary when everything is healthy or completed normally>\n")
	b.WriteString("- [[schedule:alert]] <one-sentence summary when an anomaly, failure, or item needs user attention>\n")
	b.WriteString("- [[schedule:blocked]] <one-sentence summary when blocked by missing access, approval, or external blocker>\n")
	return b.String()
}

// ParseVerdict extracts the structured status marker from the assistant reply.
// If the model did not emit a marker, the run settles to "ok" with a compact
// redacted excerpt so normal conversational prompts still work seamlessly.
func ParseVerdict(reply string) (status, summary string) {
	cleaned := diagnostics.RedactSecrets(strings.TrimSpace(reply))
	if cleaned == "" {
		return StatusOK, ""
	}
	lines := strings.Split(cleaned, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, verdictAlertPrefix):
			return StatusAlert, truncateRunes(strings.TrimSpace(line[len(verdictAlertPrefix):]), MaxSummaryRunes)
		case strings.HasPrefix(lower, verdictBlockedPrefix):
			return StatusBlocked, truncateRunes(strings.TrimSpace(line[len(verdictBlockedPrefix):]), MaxSummaryRunes)
		case strings.HasPrefix(lower, verdictOKPrefix):
			return StatusOK, truncateRunes(strings.TrimSpace(line[len(verdictOKPrefix):]), MaxSummaryRunes)
		}
	}
	return StatusOK, truncateRunes(cleaned, MaxSummaryRunes)
}
