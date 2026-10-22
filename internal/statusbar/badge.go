package statusbar

import "strconv"

// DockBadgeLabel returns the text rendered on the macOS Dock tile. The badge
// only reports items that need the user (approvals, questions, stalled sessions
// and degraded providers are all folded into Attention); running sessions stay
// in the menu bar so the Dock does not stay noisy while agents work.
func (s Summary) DockBadgeLabel() string {
	if !s.Available || s.Attention <= 0 {
		return ""
	}
	if s.Attention > 99 {
		return "99+"
	}
	return strconv.Itoa(s.Attention)
}
