package agent

import (
	"errors"
	"strings"
	"unicode"
)

const (
	chantChunkRunes       = 50
	chantRepeatThreshold  = 10
	chantMaxBufferRunes   = 5000
	chantMaxAvgGapFactor  = 5
	chantMinDistinctRunes = 6
	chantMaxLineTrackLen  = 256
)

var errStreamChantingLoop = errors.New("model entered a repetitive output loop")

// streamChantingDetector detects token-level chanting degeneration in a single
// streaming assistant response using a bounded rune sliding window with exact
// substring verification. Fenced code blocks and structural divider lines are
// ignored so legitimate formatted output never false-positives.
type streamChantingDetector struct {
	buf         []rune
	stats       map[uint64][]int
	nextScan    int
	inCodeBlock bool
	lineBuf     strings.Builder
	fenceChar   rune
}

func newStreamChantingDetector() *streamChantingDetector {
	return &streamChantingDetector{
		stats: make(map[uint64][]int, 256),
	}
}

func (d *streamChantingDetector) Reset() {
	if d == nil {
		return
	}
	d.resetWindow()
	d.inCodeBlock = false
	d.fenceChar = 0
	d.lineBuf.Reset()
}

func (d *streamChantingDetector) resetWindow() {
	d.buf = d.buf[:0]
	clear(d.stats)
	d.nextScan = 0
}

// Observe ingests one streamed text/thinking delta and reports whether the
// stream has entered a tightly clustered repetitive chanting loop.
func (d *streamChantingDetector) Observe(delta string) (bool, string) {
	if d == nil || delta == "" {
		return false, ""
	}
	for _, r := range delta {
		if r == '\r' {
			continue
		}
		if r == '\n' {
			line := d.lineBuf.String()
			d.lineBuf.Reset()
			if d.handleCompletedLine(line) {
				continue
			}
			if !d.inCodeBlock {
				d.appendRune('\n')
			}
			continue
		}
		if d.lineBuf.Len() < chantMaxLineTrackLen {
			d.lineBuf.WriteRune(r)
		}
		if !d.inCodeBlock {
			// Check whether the current line just opened a fence (``` or ~~~)
			// before scanning any text past it.
			if isFenceLinePrefix(d.lineBuf.String()) {
				continue
			}
			d.appendRune(r)
		}
	}
	if d.inCodeBlock {
		return false, ""
	}
	d.trimBuffer()
	return d.scanForLoop()
}

func isFenceLinePrefix(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed == "```" || trimmed == "~~~"
}

func (d *streamChantingDetector) handleCompletedLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) >= 3 && (strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")) {
		ch := rune(trimmed[0])
		if !d.inCodeBlock {
			d.inCodeBlock = true
			d.fenceChar = ch
		} else if d.fenceChar == ch {
			d.inCodeBlock = false
			d.fenceChar = 0
		}
		d.resetWindow()
		return true
	}
	if d.inCodeBlock {
		return true
	}
	if isChantDividerLine(trimmed) {
		d.resetWindow()
		return true
	}
	return false
}

func isChantDividerLine(trimmed string) bool {
	if len(trimmed) < 3 {
		return false
	}
	hasSym := false
	for _, r := range trimmed {
		switch {
		case unicode.IsSpace(r):
			continue
		case r == '-' || r == '=' || r == '_' || r == '*' || r == '+' || r == '|' || r == ':' || (r >= 0x2500 && r <= 0x257F):
			hasSym = true
		default:
			return false
		}
	}
	return hasSym
}

func (d *streamChantingDetector) appendRune(r rune) {
	d.buf = append(d.buf, r)
}

func (d *streamChantingDetector) trimBuffer() {
	if len(d.buf) <= chantMaxBufferRunes {
		return
	}
	drop := len(d.buf) - chantMaxBufferRunes
	copy(d.buf, d.buf[drop:])
	d.buf = d.buf[:chantMaxBufferRunes]
	if d.nextScan > drop {
		d.nextScan -= drop
	} else {
		d.nextScan = 0
	}
	for h, idxs := range d.stats {
		w := 0
		for _, pos := range idxs {
			if pos >= drop {
				idxs[w] = pos - drop
				w++
			}
		}
		if w == 0 {
			delete(d.stats, h)
		} else {
			d.stats[h] = idxs[:w]
		}
	}
}

func (d *streamChantingDetector) scanForLoop() (bool, string) {
	for d.nextScan+chantChunkRunes <= len(d.buf) {
		chunk := d.buf[d.nextScan : d.nextScan+chantChunkRunes]
		if hasSufficientRuneDiversity(chunk) {
			h := hashRuneChunk(chunk)
			idxs := d.stats[h]
			if len(idxs) == 0 {
				d.stats[h] = []int{d.nextScan}
			} else if runeSlicesEqual(chunk, d.buf[idxs[0]:idxs[0]+chantChunkRunes]) {
				idxs = append(idxs, d.nextScan)
				d.stats[h] = idxs
				if len(idxs) >= chantRepeatThreshold {
					recent := idxs[len(idxs)-chantRepeatThreshold:]
					span := recent[len(recent)-1] - recent[0]
					maxSpan := chantChunkRunes * chantMaxAvgGapFactor * (chantRepeatThreshold - 1)
					if span <= maxSpan {
						return true, strings.TrimSpace(string(chunk))
					}
				}
			}
		}
		d.nextScan++
	}
	return false, ""
}

func hasSufficientRuneDiversity(chunk []rune) bool {
	var seen [chantMinDistinctRunes]rune
	count := 0
	for _, r := range chunk {
		if unicode.IsSpace(r) {
			continue
		}
		dup := false
		for i := 0; i < count; i++ {
			if seen[i] == r {
				dup = true
				break
			}
		}
		if !dup {
			seen[count] = r
			count++
			if count >= chantMinDistinctRunes {
				return true
			}
		}
	}
	return false
}

func hashRuneChunk(chunk []rune) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	var h uint64 = offset64
	for _, r := range chunk {
		h ^= uint64(r)
		h *= prime64
	}
	return h
}

func runeSlicesEqual(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func canRecoverThinkingChant(streamed stepStreamResult, chantingRecoveries, step, maxSteps int) bool {
	return streamed.chantingLoop &&
		streamed.chantingInThinking &&
		streamed.visibleTextLen == 0 &&
		len(streamed.toolCalls) == 0 &&
		chantingRecoveries == 0 &&
		step < maxSteps
}

func appendThinkingChantRecoveryMessages(msgs []Message) []Message {
	return append(msgs,
		Message{
			Role:    RoleAssistant,
			Content: []ContentBlock{{Type: ContentText, Text: "[reasoning stopped after entering a repetitive loop]"}},
		},
		Message{
			Role: RoleUser,
			Content: []ContentBlock{{
				Type: ContentText,
				Text: "[System notice: your previous reasoning entered a repetitive loop and was stopped early. Proceed concisely with the next concrete tool call or your final response without repeating yourself.]",
			}},
		},
	)
}
