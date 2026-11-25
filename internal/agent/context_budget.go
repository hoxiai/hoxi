package agent

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/hoxiai/hoxi/internal/diagnostics"
	"github.com/hoxiai/hoxi/internal/llm"
)

const (
	tokenUnitsDivisor = 15
	mediaBlockUnits   = 1600 * tokenUnitsDivisor

	// Pricing-cliff models (e.g. Claude / Gemini with 2x >200K input pricing)
	// stay safely below the 200K cliff when measured in real tokens.
	cliffRegularCompactTriggerTokens = 160000
	cliffRegularCompactHardCapTokens = 185000

	// Flat-pricing medium large windows (256K–400K) and ultra-large windows
	// (>400K, e.g. 1M Codex/GPT-5/6) preserve warm prompt caches much longer
	// while still bounding latency and attention decay well below raw capacity.
	mediumWindowCompactTriggerTokens = 220000
	mediumWindowCompactHardCapTokens = 280000
	largeWindowCompactTriggerTokens  = 280000
	largeWindowCompactHardCapTokens  = 360000

	minContextScaleRatio = 0.55
	maxContextScaleRatio = 1.35
	minCalibrationTokens = 500
)

// estimateTextUnits weights ASCII bytes at 4 units (~3.75 bytes/token when
// divided by 15) and non-ASCII UTF-8 bytes at 5 units (~3 bytes / 1 CJK rune
// per token), avoiding both non-ASCII double-counting and the ~25% ASCII
// overestimation of a uniform len/3 heuristic.
func estimateTextUnits(s string) int {
	nonASCII := 0
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			nonASCII++
		}
	}
	return len(s)*4 + nonASCII
}

func estimateMessageUnits(m Message, inActiveTurn bool) int {
	units := 0
	for _, b := range m.Content {
		switch b.Type {
		case ContentThinking:
			// Unsigned prior-turn thinking is stripped on OpenAI chat relays and
			// not replayed on Anthropic; signed blocks carry encrypted signature cost.
			if inActiveTurn || b.Signature != "" || b.Data != "" {
				units += estimateTextUnits(b.Text) + len(b.Signature)*2 + len(b.Data)*2 + 64
			}
		case ContentMedia:
			units += estimateTextUnits(b.Text) + 64
			if b.Data != "" {
				// Realistic multimodal token budget (~1600 tokens) for images/PDFs.
				units += mediaBlockUnits
			}
		default:
			units += estimateTextUnits(b.Text) + estimateTextUnits(string(b.Input)) + 64
		}
	}
	return units
}

func estimateTokens(msgs []Message, system string) int {
	units := estimateTextUnits(system)
	activeTurnStart := lastPlainUserMessageIndex(msgs)
	for msgIdx, m := range msgs {
		inActiveTurn := activeTurnStart < 0 || msgIdx >= activeTurnStart
		units += estimateMessageUnits(m, inActiveTurn)
	}
	return units/tokenUnitsDivisor + 64
}

func estimateToolSpecsTokens(specs []ToolSpec) int {
	if len(specs) == 0 {
		return 0
	}
	units := 0
	for _, s := range specs {
		units += estimateTextUnits(s.Name) + estimateTextUnits(s.Description) + 96
		if len(s.Parameters) > 0 {
			if raw, err := json.Marshal(s.Parameters); err == nil {
				units += estimateTextUnits(string(raw))
			}
		}
	}
	return units / tokenUnitsDivisor
}

func effectiveCompactContextLimit(contextLimit int, specs []ToolSpec) int {
	if contextLimit <= 0 {
		contextLimit = 128000
	}
	return max(1, contextLimit-estimateToolSpecsTokens(specs))
}

type turnContextThresholds struct {
	contextLimit  int
	outputReserve int
	triggerTokens int
	hardCapTokens int
}

// hasLongContextPricingCliff reports whether modelID belongs to a model family
// that typically applies a 2x long-context pricing cliff above 200K tokens.
func hasLongContextPricingCliff(modelID string) bool {
	lower := strings.ToLower(strings.TrimSpace(modelID))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "claude") ||
		strings.Contains(lower, "gemini-2.5") ||
		strings.Contains(lower, "gemini-3") ||
		strings.Contains(lower, "gemini-4")
}

// resolveTurnContextThresholds computes real-token pruning and proactive
// compaction ceilings scaled to the model's actual context window and pricing
// cliff profile, always leaving room for output generation.
func resolveTurnContextThresholds(target llm.StreamTarget) turnContextThresholds {
	contextLimit := target.ContextLimit
	if contextLimit <= 0 {
		contextLimit = 128000
	}
	outLimit := target.OutputLimit
	if outLimit <= 0 {
		outLimit = 8192
	}
	// Reserve generation headroom without letting a huge declared max-output
	// (e.g. 128K on a 200K model) collapse the usable input window.
	maxReserve := max(8192, contextLimit/5)
	outputReserve := min(outLimit, maxReserve)
	if outputReserve >= contextLimit {
		outputReserve = contextLimit / 4
	}

	var triggerCap, hardCap int
	switch {
	case contextLimit <= 200000:
		triggerCap = contextLimit * compactTriggerPercent / 100
		hardCap = contextLimit * compactHardCapPercent / 100
	case hasLongContextPricingCliff(target.ModelID):
		triggerCap = min(contextLimit*compactTriggerPercent/100, cliffRegularCompactTriggerTokens)
		hardCap = min(contextLimit*compactHardCapPercent/100, cliffRegularCompactHardCapTokens)
	case contextLimit <= 400000:
		triggerCap = min(contextLimit*compactTriggerPercent/100, mediumWindowCompactTriggerTokens)
		hardCap = min(contextLimit*85/100, mediumWindowCompactHardCapTokens)
	default:
		triggerCap = min(contextLimit*compactTriggerPercent/100, largeWindowCompactTriggerTokens)
		hardCap = min(contextLimit*85/100, largeWindowCompactHardCapTokens)
	}

	usableInput := max(1, contextLimit-outputReserve)
	if hardCap > usableInput {
		hardCap = usableInput
	}
	if triggerCap >= hardCap && hardCap > 2048 {
		triggerCap = hardCap * 85 / 100
	}
	if triggerCap <= 0 {
		triggerCap = max(1, contextLimit*compactTriggerPercent/100)
	}
	if hardCap <= 0 {
		hardCap = max(triggerCap, contextLimit*compactHardCapPercent/100)
	}
	return turnContextThresholds{
		contextLimit:  contextLimit,
		outputReserve: outputReserve,
		triggerTokens: triggerCap,
		hardCapTokens: hardCap,
	}
}

// contextTokenTracker anchors context accounting to provider-reported prompt
// token usage from the latest completed main model step, adding calibrated
// estimates only for messages appended after that step.
type prefixFingerprint struct {
	valid        bool
	systemHash   [32]byte
	toolsHash    [32]byte
	cacheKeyHash [32]byte
	msgHashes    [][32]byte
	cacheRead    int
}

type contextTokenTracker struct {
	anchored        bool
	anchorPrompt    int
	anchorMsgCount  int
	anchorSystem    string
	anchorToolCount int
	scale           float64
	prefixCacheWarm bool
	lastUsage       TokenUsage
	lastFingerprint prefixFingerprint
}

func newContextTokenTracker() *contextTokenTracker {
	return &contextTokenTracker{}
}

func (t *contextTokenTracker) Scale() float64 {
	if t == nil || t.scale <= 0 {
		return 1.0
	}
	return t.scale
}

func (t *contextTokenTracker) IsPrefixCacheWarm() bool {
	return t != nil && t.prefixCacheWarm
}

func (t *contextTokenTracker) InvalidateAnchor(clearCacheWarm bool) {
	if t == nil {
		return
	}
	t.anchored = false
	t.anchorPrompt = 0
	t.anchorMsgCount = 0
	if clearCacheWarm {
		t.prefixCacheWarm = false
	}
}

// RebaseAfterPrune updates an anchored prompt-token estimate after in-place
// tool-result pruning mutates older blocks. It preserves provider-measured
// accuracy (subtracting only the calibrated heuristic delta of pruned content)
// so proactive compaction uses the same token meter as pruning, while clearing
// prefixCacheWarm because the mutated prefix no longer matches the warm cache.
func (t *contextTokenTracker) RebaseAfterPrune(preMsgs, postMsgs []Message, fullSystem string, toolSpecs []ToolSpec) {
	if t == nil {
		return
	}
	t.prefixCacheWarm = false
	if !t.anchored {
		return
	}
	if len(postMsgs) == 0 {
		t.InvalidateAnchor(true)
		return
	}
	preTotal := t.EstimateTotalTokens(preMsgs, fullSystem, toolSpecs)
	delta := estimateTokens(preMsgs, fullSystem) - estimateTokens(postMsgs, fullSystem)
	switch {
	case delta > 0:
		preTotal = max(0, preTotal-scaleTokenCount(delta, t.Scale()))
	case delta < 0:
		preTotal += scaleTokenCount(-delta, t.Scale())
	}
	t.anchorPrompt = preTotal
	t.anchorMsgCount = len(postMsgs)
	t.anchorSystem = fullSystem
	t.anchorToolCount = len(toolSpecs)
}

// ObserveStep records exact provider-reported prompt usage for the request
// that just completed, updating both the exact prefix anchor and the
// per-session tokenizer density calibration ratio.
func (t *contextTokenTracker) ObserveStep(sentMsgs []Message, fullSystem string, toolSpecs []ToolSpec, u TokenUsage) {
	if t == nil {
		return
	}
	promptTokens := u.Input + u.CacheRead + u.CacheWrite
	if promptTokens <= 0 {
		return
	}
	t.lastUsage = u
	t.prefixCacheWarm = u.CacheRead > 0 || u.CacheWrite > 0
	t.anchored = true
	t.anchorPrompt = promptTokens
	t.anchorMsgCount = len(sentMsgs)
	t.anchorSystem = fullSystem
	t.anchorToolCount = len(toolSpecs)

	estPrompt := estimateTokens(sentMsgs, fullSystem) + estimateToolSpecsTokens(toolSpecs)
	if estPrompt >= minCalibrationTokens {
		ratio := float64(promptTokens) / float64(estPrompt)
		ratio = clampFloat(ratio, minContextScaleRatio, maxContextScaleRatio)
		if t.scale > 0 {
			t.scale = 0.7*ratio + 0.3*t.scale
		} else {
			t.scale = ratio
		}
	}
}

// EstimateTotalTokens returns the best available total input token count
// (tools + system + messages). When anchored to the latest provider usage on
// an unchanged prefix, only newly appended messages are estimated.
func (t *contextTokenTracker) EstimateTotalTokens(msgs []Message, fullSystem string, toolSpecs []ToolSpec) int {
	if t != nil && t.anchored &&
		fullSystem == t.anchorSystem &&
		len(toolSpecs) == t.anchorToolCount &&
		len(msgs) >= t.anchorMsgCount {
		delta := estimateDeltaMessagesTokens(msgs, t.anchorMsgCount)
		return t.anchorPrompt + scaleTokenCount(delta, t.Scale())
	}
	est := estimateTokens(msgs, fullSystem) + estimateToolSpecsTokens(toolSpecs)
	return scaleTokenCount(est, t.Scale())
}

func estimateDeltaMessagesTokens(msgs []Message, startIdx int) int {
	if startIdx < 0 {
		startIdx = 0
	}
	if startIdx >= len(msgs) {
		return 0
	}
	activeTurnStart := lastPlainUserMessageIndex(msgs)
	units := 0
	for i := startIdx; i < len(msgs); i++ {
		inActiveTurn := activeTurnStart < 0 || i >= activeTurnStart
		units += estimateMessageUnits(msgs[i], inActiveTurn)
	}
	return units / tokenUnitsDivisor
}

func scaleTokenCount(tokens int, scale float64) int {
	if tokens <= 0 {
		return 0
	}
	if scale <= 0 || scale == 1.0 {
		return tokens
	}
	return int(math.Round(float64(tokens) * scale))
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// pruneOldToolResultsWithBudget applies quantized tool-result pruning driven
// by provider-measured context tokens and window-aware thresholds.
func pruneOldToolResultsWithBudget(
	msgs []Message,
	fullSystem, worktreeDir string,
	toolSpecs []ToolSpec,
	thresholds turnContextThresholds,
	tracker *contextTokenTracker,
) ([]Message, bool) {
	if len(msgs) == 0 {
		return msgs, false
	}
	baseTotal := tracker.EstimateTotalTokens(msgs, fullSystem, toolSpecs)
	baseHeuristic := estimateTokens(msgs, fullSystem)
	scale := tracker.Scale()
	measure := func(cand []Message) int {
		if len(cand) == len(msgs) && len(cand) > 0 && &cand[0] == &msgs[0] {
			return baseTotal
		}
		candHeuristic := estimateTokens(cand, fullSystem)
		delta := baseHeuristic - candHeuristic
		if delta == 0 {
			return baseTotal
		}
		return max(0, baseTotal-scaleTokenCount(delta, scale))
	}
	pruned := pruneOldToolResultsCore(
		msgs,
		fullSystem,
		worktreeDir,
		thresholds.contextLimit,
		thresholds.triggerTokens,
		thresholds.hardCapTokens,
		measure,
	)
	changed := messagesSliceMutated(msgs, pruned)
	return pruned, changed
}

func messagesSliceMutated(before, after []Message) bool {
	if len(before) != len(after) {
		return true
	}
	for i := range before {
		if !messageBlocksIdentical(before[i], after[i]) {
			return true
		}
	}
	return false
}

// shouldProactivelyCompactWithBudget checks whether post-pruning context still
// exceeds the model's real-token hard cap and has substantial older history
// before the trailing anchor.
func shouldProactivelyCompactWithBudget(
	msgs []Message,
	fullSystem string,
	toolSpecs []ToolSpec,
	thresholds turnContextThresholds,
	tracker *contextTokenTracker,
	rawContextLimit, historyLen, currentStep int,
) bool {
	if rawContextLimit < minProactiveCompactContextLimit || len(msgs) < 4 {
		return false
	}
	if tracker.EstimateTotalTokens(msgs, fullSystem, toolSpecs) <= thresholds.hardCapTokens {
		return false
	}
	tailStart, _ := compactionTailAnchor(msgs, historyLen, currentStep)
	if tailStart < 2 {
		return false
	}
	olderTokens := scaleTokenCount(estimateTokens(msgs[:tailStart], ""), tracker.Scale())
	return olderTokens >= max(1024, thresholds.hardCapTokens/4)
}

func compactionTailAnchor(msgs []Message, historyLen, currentStep int) (tailStart int, keptPriorToolPair bool) {
	tailStart = len(msgs) - 1
	if currentStep <= 1 && historyLen >= 2 && historyLen < len(msgs) {
		return historyLen, false
	}
	if hasToolResultBlocks(msgs[tailStart]) && tailStart > 0 && msgs[tailStart-1].Role == RoleAssistant {
		return tailStart - 1, true
	}
	return tailStart, false
}

const minCacheDropBaselineTokens = 2048

func computePrefixFingerprint(cacheKey, fullSystem string, toolSpecs []ToolSpec, msgs []Message, cacheRead int) prefixFingerprint {
	fp := prefixFingerprint{
		valid:        true,
		systemHash:   sha256.Sum256([]byte(fullSystem)),
		cacheKeyHash: sha256.Sum256([]byte(cacheKey)),
		msgHashes:    make([][32]byte, len(msgs)),
		cacheRead:    cacheRead,
	}
	th := sha256.New()
	var lenBuf [8]byte
	for _, spec := range toolSpecs {
		raw, _ := json.Marshal(spec)
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(raw)))
		th.Write(lenBuf[:])
		th.Write(raw)
	}
	copy(fp.toolsHash[:], th.Sum(nil))
	for i, m := range msgs {
		mh := sha256.New()
		mh.Write([]byte(m.Role))
		mh.Write([]byte{0})
		for _, b := range m.Content {
			mh.Write([]byte(b.Type))
			mh.Write([]byte{0})
			for _, part := range []string{b.ID, b.Name, b.ToolUseID, b.Text, b.Signature, b.MimeType, b.Filename} {
				binary.BigEndian.PutUint64(lenBuf[:], uint64(len(part)))
				mh.Write(lenBuf[:])
				mh.Write([]byte(part))
			}
			binary.BigEndian.PutUint64(lenBuf[:], uint64(len(b.Input)))
			mh.Write(lenBuf[:])
			mh.Write(b.Input)
			binary.BigEndian.PutUint64(lenBuf[:], uint64(len(b.Data)))
			mh.Write(lenBuf[:])
			mh.Write([]byte(b.Data))
			if b.IsError {
				mh.Write([]byte{1})
			} else {
				mh.Write([]byte{0})
			}
		}
		copy(fp.msgHashes[i][:], mh.Sum(nil))
	}
	return fp
}

func classifyPrefixCacheChange(prev, cur prefixFingerprint) (cause string, sharedMsgs int) {
	limit := min(len(prev.msgHashes), len(cur.msgHashes))
	for sharedMsgs < limit && prev.msgHashes[sharedMsgs] == cur.msgHashes[sharedMsgs] {
		sharedMsgs++
	}
	var reasons []string
	if prev.cacheKeyHash != cur.cacheKeyHash {
		reasons = append(reasons, "cache_key_changed")
	}
	if prev.systemHash != cur.systemHash {
		reasons = append(reasons, "system_changed")
	}
	if prev.toolsHash != cur.toolsHash {
		reasons = append(reasons, "tools_changed")
	}
	if sharedMsgs < len(prev.msgHashes) {
		reasons = append(reasons, fmt.Sprintf("history_mutated_at=%d/%d", sharedMsgs, len(prev.msgHashes)))
	}
	if len(reasons) == 0 {
		return "upstream_evicted_prefix_unchanged", sharedMsgs
	}
	return strings.Join(reasons, "+"), sharedMsgs
}

// ObservePrefixCacheStability compares SHA-256 digests of the current step's
// prompt prefix against the previous step without retaining any raw prompt or
// message bodies. When a previously warm prompt cache drops by >50%, it logs
// a metadata-only diagnostic pinpointing whether Hoxi mutated the prefix or
// the upstream cache evicted an unchanged prefix.
func (t *contextTokenTracker) ObservePrefixCacheStability(
	sessionID string,
	target llm.StreamTarget,
	cacheKey, fullSystem string,
	toolSpecs []ToolSpec,
	sentMsgs []Message,
	u TokenUsage,
) {
	if t == nil {
		return
	}
	cur := computePrefixFingerprint(cacheKey, fullSystem, toolSpecs, sentMsgs, u.CacheRead)
	prev := t.lastFingerprint
	t.lastFingerprint = cur
	if !prev.valid || prev.cacheRead < minCacheDropBaselineTokens || cur.cacheRead >= prev.cacheRead/2 {
		return
	}
	cause, shared := classifyPrefixCacheChange(prev, cur)
	diagnostics.Record(diagnostics.Entry{
		Kind:       "cache",
		Level:      "warn",
		Source:     "agent",
		SessionID:  sessionID,
		ProviderID: target.ProviderID,
		ModelID:    target.ModelID,
		Message:    fmt.Sprintf("prompt cache drop: %s (cacheRead %d -> %d, sharedMsgs %d/%d)", cause, prev.cacheRead, cur.cacheRead, shared, len(prev.msgHashes)),
	})
}
