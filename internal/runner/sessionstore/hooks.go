package sessionstore

import "sync"

// TranscriptHooks plugs optional runtime-specific transcript hydration and
// tool-part sanitation into Store without coupling sessionstore to CLI drivers.
type TranscriptHooks struct {
	HydrateEdits        func(externalID string, turns []MessageTurn) bool
	HydrateTurnTokens   func(externalID string, turns []MessageTurn) bool
	PreserveEditInput   func(incoming *MessagePart, existing MessagePart)
	PruneDuplicateTools func(parts []MessagePart) []MessagePart
}

var (
	hooksMu      sync.RWMutex
	defaultHooks TranscriptHooks
)

// RegisterTranscriptHooks sets the process-wide transcript hooks used when
// loading, indexing, or updating conversation turns.
func RegisterTranscriptHooks(h TranscriptHooks) {
	hooksMu.Lock()
	defaultHooks = h
	hooksMu.Unlock()
}

func currentHooks() TranscriptHooks {
	hooksMu.RLock()
	h := defaultHooks
	hooksMu.RUnlock()
	return h
}

func hydrateEditParts(externalID string, turns []MessageTurn) bool {
	if h := currentHooks(); h.HydrateEdits != nil {
		return h.HydrateEdits(externalID, turns)
	}
	return false
}

func hydrateTurnTokens(externalID string, turns []MessageTurn) bool {
	if h := currentHooks(); h.HydrateTurnTokens != nil {
		return h.HydrateTurnTokens(externalID, turns)
	}
	return false
}

func preserveEditInput(incoming *MessagePart, existing MessagePart) {
	if h := currentHooks(); h.PreserveEditInput != nil {
		h.PreserveEditInput(incoming, existing)
	}
}

func pruneDuplicateToolParts(parts []MessagePart) []MessagePart {
	if h := currentHooks(); h.PruneDuplicateTools != nil {
		return h.PruneDuplicateTools(parts)
	}
	return parts
}
