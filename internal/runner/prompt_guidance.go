package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
)

const (
	cliSystemCmdGuidanceDigestKey = "cmd-guidance:v1"
	maxPersistedCLISystemDigests  = 64
)

// nativeCLISystemForTurn avoids repeating commandExecutionGuidance and already-
// delivered caller system blocks inside user messages of a resumed native CLI
// conversation (--resume / --conversation), where an earlier turn already placed
// them in the CLI's persistent history. Delivered block digests are persisted on
// SessionRecord so restarts and partial callerSystem changes (e.g. store switch
// or hot-reloaded app context) only emit newly added or modified blocks rather
// than re-appending multi-kilobyte mode prompts.
func (s *Service) nativeCLISystemForTurn(rec SessionRecord, callerSystem, fullSystem string) string {
	if strings.TrimSpace(rec.ExternalID) == "" || rec.ContextPending {
		return fullSystem
	}
	delivered, hasPrior := s.deliveredCLISystemDigestSet(rec)
	if !hasPrior {
		return fullSystem
	}
	if strings.TrimSpace(callerSystem) == "" {
		return ""
	}
	blocks := splitCLISystemBlocks(callerSystem)
	if len(blocks) == 0 {
		return ""
	}
	var pending []string
	for _, blk := range blocks {
		if _, seen := delivered[cliSystemBlockToken(blk)]; !seen {
			pending = append(pending, blk)
		}
	}
	return strings.Join(pending, "\n\n")
}

func (s *Service) deliveredCLISystemDigestSet(rec SessionRecord) (map[string]struct{}, bool) {
	key := rec.ID + "\x00" + rec.Runtime
	delivered := make(map[string]struct{})
	addSlice := func(items []string) bool {
		if len(items) == 0 {
			return false
		}
		for _, d := range items {
			if d != "" {
				delivered[d] = struct{}{}
			}
		}
		return len(delivered) > 0
	}

	// Prefer live in-memory digests (authoritative for current process), then
	// persisted digests on SessionRecord (survives restarts), then legacy raw
	// cliSystemDelivered fallback.
	if digests, ok := s.cliSystemDigests.Load(key); ok && addSlice(digests) {
		return delivered, true
	}
	if addSlice(rec.CLISystemDigests[rec.Runtime]) {
		return delivered, true
	}
	if prevCaller, ok := s.cliSystemDelivered.Load(key); ok {
		delivered[cliSystemCmdGuidanceDigestKey] = struct{}{}
		for _, blk := range splitCLISystemBlocks(prevCaller) {
			delivered[cliSystemBlockToken(blk)] = struct{}{}
		}
		return delivered, true
	}
	return delivered, false
}

func (s *Service) recordCLISystemDelivered(sessionID, runtime, callerSystem string) {
	s.recordCLISystemDeliveredWithReset(sessionID, runtime, callerSystem, false)
}

func (s *Service) recordCLISystemDeliveredForTurn(rec SessionRecord, callerSystem string) {
	fresh := strings.TrimSpace(rec.ExternalID) == "" || rec.ContextPending
	s.recordCLISystemDeliveredWithReset(rec.ID, rec.Runtime, callerSystem, fresh)
}

func (s *Service) recordCLISystemDeliveredWithReset(sessionID, runtime, callerSystem string, reset bool) {
	sessionID = strings.TrimSpace(sessionID)
	runtime = strings.TrimSpace(runtime)
	if sessionID == "" || runtime == "" {
		return
	}
	key := sessionID + "\x00" + runtime
	s.cliSystemDelivered.Store(key, callerSystem)

	var base []string
	if !reset {
		if cur, ok := s.cliSystemDigests.Load(key); ok {
			base = append(base, cur...)
		} else if s.store != nil {
			if meta, exists := s.store.Meta(sessionID); exists {
				base = append(base, meta.CLISystemDigests[runtime]...)
			}
		}
	}
	next := mergeCLISystemDigests(base, callerSystem)
	s.cliSystemDigests.Store(key, next)

	if s.store != nil {
		if meta, exists := s.store.Meta(sessionID); exists && !slices.Equal(meta.CLISystemDigests[runtime], next) {
			_, _, _ = s.store.UpdateMetadata(sessionID, func(sm *SessionMetadata) {
				if sm.CLISystemDigests == nil {
					sm.CLISystemDigests = make(map[string][]string, 1)
				}
				cp := make([]string, len(next))
				copy(cp, next)
				sm.CLISystemDigests[runtime] = cp
			})
		}
	}
}

func (s *Service) fallbackCLISystemForFreshRetry(sessionID, runtime string) string {
	sys := effectiveCommandExecutionGuidance()
	if caller, ok := s.cliSystemDelivered.Load(sessionID + "\x00" + runtime); ok {
		if strings.TrimSpace(caller) != "" {
			sys += "\n\n" + strings.TrimSpace(caller)
		}
	}
	return sys
}

func mergeCLISystemDigests(existing []string, callerSystem string) []string {
	out := make([]string, 0, len(existing)+4)
	posByKey := make(map[string]int, len(existing)+4)
	upsert := func(token string) {
		if token == "" {
			return
		}
		// Tagged blocks ("tag:<name>=<hash>") replace prior values for the same
		// tag so switching active state A -> B -> A always re-delivers A.
		slotKey := token
		if strings.HasPrefix(token, "tag:") {
			if eq := strings.IndexByte(token, '='); eq > 4 {
				slotKey = token[:eq]
			}
		}
		if idx, exists := posByKey[slotKey]; exists {
			out[idx] = token
			return
		}
		posByKey[slotKey] = len(out)
		out = append(out, token)
	}
	upsert(cliSystemCmdGuidanceDigestKey)
	for _, d := range existing {
		upsert(d)
	}
	for _, blk := range splitCLISystemBlocks(callerSystem) {
		upsert(cliSystemBlockToken(blk))
	}
	if len(out) > maxPersistedCLISystemDigests {
		// Keep the command-guidance sentinel at index 0 plus the most recent block tokens.
		trimmed := make([]string, 0, maxPersistedCLISystemDigests)
		trimmed = append(trimmed, cliSystemCmdGuidanceDigestKey)
		trimmed = append(trimmed, out[len(out)-(maxPersistedCLISystemDigests-1):]...)
		out = trimmed
	}
	return out
}

func cliSystemBlockToken(block string) string {
	digest := cliSystemBlockDigest(block)
	if tag, ok := leadingTaggedBlockName(block); ok {
		return "tag:" + tag + "=" + digest
	}
	return "blk:" + digest
}

func leadingTaggedBlockName(block string) (string, bool) {
	trimmed := strings.TrimSpace(block)
	if _, ok := matchLeadingTaggedBlockEnd(trimmed); !ok {
		return "", false
	}
	i := 1
	for i < len(trimmed) && (isASCIIAlpha(trimmed[i]) || (trimmed[i] >= '0' && trimmed[i] <= '9') || trimmed[i] == '_' || trimmed[i] == '-') {
		i++
	}
	tag := strings.ToLower(trimmed[1:i])
	if tag == "" {
		return "", false
	}
	return tag, true
}

func cliSystemBlockDigest(block string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(block)))
	return hex.EncodeToString(sum[:8])
}

// splitCLISystemBlocks segments callerSystem into atomic top-level tagged
// sections (<tag>...</tag>, preserving internal blank lines) and blank-line-
// delimited prose paragraphs so updating one dynamic section never forces
// unchanged static sections (like app-dev mode rules) to be re-sent.
func splitCLISystemBlocks(s string) []string {
	rem := strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if rem == "" {
		return nil
	}
	var blocks []string
	for rem != "" {
		rem = strings.TrimLeft(rem, " \t\n")
		if rem == "" {
			break
		}
		if endIdx, ok := matchLeadingTaggedBlockEnd(rem); ok {
			if blk := strings.TrimSpace(rem[:endIdx]); blk != "" {
				blocks = append(blocks, blk)
			}
			rem = rem[endIdx:]
			continue
		}
		// Plain paragraph: read until blank line (\n\n) or a newline starting a closed tagged block.
		splitAt := len(rem)
		for i := 0; i < len(rem); i++ {
			if rem[i] != '\n' {
				continue
			}
			if i+1 < len(rem) && rem[i+1] == '\n' {
				splitAt = i
				break
			}
			if i+1 < len(rem) {
				if _, ok := matchLeadingTaggedBlockEnd(rem[i+1:]); ok {
					splitAt = i
					break
				}
			}
		}
		if blk := strings.TrimSpace(rem[:splitAt]); blk != "" {
			blocks = append(blocks, blk)
		}
		rem = rem[splitAt:]
	}
	return blocks
}

func matchLeadingTaggedBlockEnd(s string) (int, bool) {
	if len(s) < 3 || s[0] != '<' || !isASCIIAlpha(s[1]) {
		return 0, false
	}
	i := 2
	for i < len(s) && (isASCIIAlpha(s[i]) || (s[i] >= '0' && s[i] <= '9') || s[i] == '_' || s[i] == '-') {
		i++
	}
	tag := s[1:i]
	if tag == "" || i >= len(s) {
		return 0, false
	}
	if s[i] != '>' && s[i] != ' ' && s[i] != '\t' {
		return 0, false
	}
	openClose := strings.IndexByte(s[i:], '>')
	if openClose < 0 {
		return 0, false
	}
	// Open tag must close on the first line (avoid matching stray '<' across paragraphs).
	if nl := strings.IndexByte(s[i:i+openClose], '\n'); nl >= 0 {
		return 0, false
	}
	afterOpen := i + openClose + 1
	closeTag := "</" + tag + ">"
	relClose := strings.Index(s[afterOpen:], closeTag)
	if relClose < 0 {
		return 0, false
	}
	return afterOpen + relClose + len(closeTag), true
}

func isASCIIAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// isAppDevPromptMode reports whether the active mode or its resolved prompt is
// the Work App development mode, where runtime packaged-app no-source-diving
// context (<hoxi_work_app>) must not be injected.
func isAppDevPromptMode(mode, agentPrompt string) bool {
	return strings.EqualFold(strings.TrimSpace(mode), "app-dev") ||
		strings.Contains(agentPrompt, "<hoxi_work_app_dev_mode>")
}

// stripTaggedPromptSection removes every matching openTag..closeTag section from
// s and collapses extra surrounding blank lines.
func stripTaggedPromptSection(s, openTag, closeTag string) string {
	if !strings.Contains(s, openTag) {
		return s
	}
	out := s
	for {
		start := strings.Index(out, openTag)
		if start < 0 {
			break
		}
		relEnd := strings.Index(out[start:], closeTag)
		if relEnd < 0 {
			break
		}
		after := start + relEnd + len(closeTag)
		left := strings.TrimSpace(out[:start])
		right := strings.TrimSpace(out[after:])
		switch {
		case left == "":
			out = right
		case right == "":
			out = left
		default:
			out = left + "\n\n" + right
		}
	}
	return strings.TrimSpace(out)
}
