// Package appbridge implements the Tier 1 domain layer for the hoxi-app/v1
// protocol: scoped application capability grants, PKCE local authorization
// handshakes, desktop approval-as-a-service queues, and loopback runtime
// endpoint discovery.
package appbridge

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	// ProtocolVersion is the canonical identifier for the Hoxi App Protocol.
	ProtocolVersion = "hoxi-app/v1"

	// Class A scopes: programmatic capabilities gated by explicit user grant.
	ScopeLLMModels         = "llm:models"
	ScopeLLMChat           = "llm:chat"
	ScopeMediaCapabilities = "media:capabilities"
	ScopeMediaImage        = "media:image"
	ScopeMediaVideo        = "media:video"
	ScopeMediaSpeech       = "media:speech"
	ScopeMediaTranscribe   = "media:transcribe"
	ScopeDocRead           = "doc:read"
	ScopeDocWrite          = "doc:write"
	ScopeToolsRead         = "tools:read"
	ScopeToolsRun          = "tools:run"
	ScopeRecallRead        = "recall:read"
	ScopeNotifySend        = "notify:send"
	ScopeApprovalRequest   = "approval:request"

	// Class B scopes: elevated capabilities requiring runtime desktop approval.
	ScopeAgentRun        = "agent:run"
	ScopeBrowserControl  = "browser:control"
	ScopeComputerControl = "computer:control"

	// Risk tiers surfaced in desktop consent UI.
	RiskStandard = "standard"
	RiskElevated = "elevated"
)

// ScopeInfo describes one valid hoxi-app/v1 capability scope.
type ScopeInfo struct {
	Name        string `json:"name"`
	RiskTier    string `json:"riskTier"`
	Description string `json:"description"`
}

var scopeCatalog = map[string]ScopeInfo{
	ScopeLLMModels:         {Name: ScopeLLMModels, RiskTier: RiskStandard, Description: "List available models and auto-routing pools"},
	ScopeLLMChat:           {Name: ScopeLLMChat, RiskTier: RiskStandard, Description: "Call configured LLM providers via Hoxi local relay"},
	ScopeMediaCapabilities: {Name: ScopeMediaCapabilities, RiskTier: RiskStandard, Description: "Inspect configured local media generation capabilities"},
	ScopeMediaImage:        {Name: ScopeMediaImage, RiskTier: RiskStandard, Description: "Generate or edit images via configured providers"},
	ScopeMediaVideo:        {Name: ScopeMediaVideo, RiskTier: RiskStandard, Description: "Generate videos via configured providers"},
	ScopeMediaSpeech:       {Name: ScopeMediaSpeech, RiskTier: RiskStandard, Description: "Synthesize speech (TTS) via configured providers"},
	ScopeMediaTranscribe:   {Name: ScopeMediaTranscribe, RiskTier: RiskStandard, Description: "Transcribe audio (ASR) via configured providers"},
	ScopeDocRead:           {Name: ScopeDocRead, RiskTier: RiskStandard, Description: "Parse and extract content from PDF/Office documents"},
	ScopeDocWrite:          {Name: ScopeDocWrite, RiskTier: RiskStandard, Description: "Generate or update PDF/Office documents in allowed workspaces"},
	ScopeToolsRead:         {Name: ScopeToolsRead, RiskTier: RiskStandard, Description: "Discover and inspect cloud tools via Hoxi tools bridge"},
	ScopeToolsRun:          {Name: ScopeToolsRun, RiskTier: RiskStandard, Description: "Execute cloud tools via Hoxi tools bridge"},
	ScopeRecallRead:        {Name: ScopeRecallRead, RiskTier: RiskStandard, Description: "Search historical session recall within allowed workspaces"},
	ScopeNotifySend:        {Name: ScopeNotifySend, RiskTier: RiskStandard, Description: "Display rate-limited desktop notifications"},
	ScopeApprovalRequest:   {Name: ScopeApprovalRequest, RiskTier: RiskStandard, Description: "Request interactive user confirmation via desktop approval UI"},
	ScopeAgentRun:          {Name: ScopeAgentRun, RiskTier: RiskElevated, Description: "Run agent tasks in isolated worktrees under desktop shellpolicy"},
	ScopeBrowserControl:    {Name: ScopeBrowserControl, RiskTier: RiskElevated, Description: "Control an app-isolated browser profile"},
	ScopeComputerControl:   {Name: ScopeComputerControl, RiskTier: RiskElevated, Description: "Inspect or interact with foreground apps under desktop approval"},
}

// AllScopes returns the sorted catalog of supported capability scopes.
func AllScopes() []ScopeInfo {
	out := make([]ScopeInfo, 0, len(scopeCatalog))
	for _, info := range scopeCatalog {
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ValidScope reports whether scope is an explicit allow-listed capability scope.
// Wildcards (such as "*" or "llm:*") are intentionally rejected.
func ValidScope(scope string) bool {
	_, ok := scopeCatalog[strings.TrimSpace(scope)]
	return ok
}

// NormalizeScopes validates, deduplicates, and sorts a non-empty list of scopes.
func NormalizeScopes(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, errors.New("at least one capability scope is required")
	}
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s := strings.TrimSpace(item)
		if s == "" {
			return nil, errors.New("empty scope is not allowed")
		}
		if strings.Contains(s, "*") {
			return nil, fmt.Errorf("wildcard scope %q is not allowed", s)
		}
		if !ValidScope(s) {
			return nil, fmt.Errorf("unknown or forbidden scope %q", s)
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("at least one valid capability scope is required")
	}
	sort.Strings(out)
	return out, nil
}

// HasScope reports whether granted includes required.
func HasScope(granted []string, required string) bool {
	req := strings.TrimSpace(required)
	if req == "" {
		return false
	}
	for _, s := range granted {
		if s == req {
			return true
		}
	}
	return false
}

// ValidAppID enforces a strict identifier format for external and Work apps:
// 1..64 characters, starting with an ASCII letter or digit, followed by
// letters, digits, '.', '_', or '-'.
func ValidAppID(id string) bool {
	id = strings.TrimSpace(id)
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		isAlphaNum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if i == 0 {
			if !isAlphaNum {
				return false
			}
			continue
		}
		if !isAlphaNum && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}
