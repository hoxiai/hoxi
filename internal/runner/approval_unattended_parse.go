package runner

import (
	"strings"
)

// The unattended parser reads one shell command the way the shell will run
// it, closely enough to see what lands in command position after quote
// removal and expansion. It does not try to decide what an expansion holds;
// it records that a word is dynamic and leaves the verdict to
// unattendedDynamicCategory. Mirrors .opencode/lib/unattended-shell.js.

// uWord is one shell word after quote and backslash removal.
type uWord struct {
	text    string // the literal parts of the word
	dynamic bool   // holds `$…`, `${…}`, `$(…)`, backticks or `$'…'`
	procSub bool   // a `<(…)` / `>(…)` path
}

// uSeg is one simple command and where its standard input comes from.
type uSeg struct {
	words        []uWord
	pipedIn      bool
	stdin        string // "", "file", "procsub", "heredoc", "herestring"
	stdinText    string // heredoc body or here-string text
	stdinDynamic bool   // an expanding heredoc, or a here-string with an expansion
}

// uScript is a parsed command with the commands nested inside it.
type uScript struct {
	segs     []*uSeg
	children []*uScript // `$(…)`, backticks, `<(…)`, `>(…)` and `${…}` bodies
	vars     map[string]bool
	// dataOnly marks an array or arithmetic body: its words are values,
	// only the substitutions inside it run.
	dataOnly bool
}

// indirectExpansion is recorded in uScript.vars for `${!name}`.
const indirectExpansion = "${!}"

type pendingHeredoc struct {
	seg    *uSeg
	delim  string
	quoted bool
	dash   bool
}

type uParser struct {
	s       string
	i       int
	out     *uScript
	cur     *uSeg
	word    strings.Builder
	inWord  bool
	dyn     bool
	proc    bool
	redir   string // "", "in", "out", "herestring": the next word is a redirect target
	pipe    bool   // the segment being built reads a pipe
	pending []pendingHeredoc
	depth   int // open `(` inside a nested `$(…)` region
}

// parseUnattendedScript parses a whole command. ok is false for input the
// parser cannot read with certainty (unbalanced quotes, an unterminated
// heredoc or substitution); the caller treats that as dynamic.
func parseUnattendedScript(s string) (*uScript, bool) {
	p := newUParser(s)
	if _, ok := p.run(false); !ok {
		return nil, false
	}
	return p.out, true
}

func newUParser(s string) *uParser {
	return &uParser{s: s, out: &uScript{vars: map[string]bool{}}, cur: &uSeg{}}
}

// run parses until the end of input or, when nested, the `)` that closes the
// region. It returns the index of that `)`.
func (p *uParser) run(nested bool) (int, bool) {
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c == '\\':
			if p.i+1 < len(p.s) {
				if p.s[p.i+1] != '\n' {
					p.addByte(p.s[p.i+1])
				}
				p.i += 2
				continue
			}
			p.i++
		case c == '\'':
			end := strings.IndexByte(p.s[p.i+1:], '\'')
			if end < 0 {
				return 0, false
			}
			p.word.WriteString(p.s[p.i+1 : p.i+1+end])
			p.inWord = true
			p.i += end + 2
		case c == '"':
			p.inWord = true
			p.i++
			if !p.readDouble('"') {
				return 0, false
			}
		case c == '$':
			if !p.readDollar() {
				return 0, false
			}
		case c == '`':
			if !p.readBacktick() {
				return 0, false
			}
		case c == ' ' || c == '\t' || c == '\r':
			p.endWord()
			p.i++
		case c == '\n':
			p.endWord()
			p.flush(false)
			p.i++
			if !p.readHeredocBodies() {
				return 0, false
			}
		case c == '#' && !p.inWord:
			for p.i < len(p.s) && p.s[p.i] != '\n' {
				p.i++
			}
		case c == ';':
			p.endWord()
			p.flush(false)
			p.i++
		case c == '&':
			p.endWord()
			switch {
			case p.peek(1) == '&':
				p.flush(false)
				p.i += 2
			case p.peek(1) == '>':
				p.redir = "out"
				p.i += 2
				if p.peek(0) == '>' {
					p.i++
				}
			default:
				p.flush(false)
				p.i++
			}
		case c == '|':
			p.endWord()
			if p.peek(1) == '|' {
				p.flush(false)
				p.i += 2
			} else {
				p.flush(true)
				p.i++
				if p.peek(0) == '&' {
					p.i++
				}
			}
		case c == '(':
			if p.inWord && strings.HasSuffix(p.word.String(), "=") {
				// `arr=(a b)`: an array assignment; its body may still expand.
				if !p.readNested(p.i+1, false, true) {
					return 0, false
				}
				p.inWord, p.dyn = true, true
				continue
			}
			p.endWord()
			if len(p.cur.words) == 0 && p.peek(1) == '(' {
				// `(( … ))` arithmetic: only nested substitutions can run.
				if !p.readNested(p.i+2, false, true) {
					return 0, false
				}
				if p.peek(0) == ')' {
					p.i++
				}
				continue
			}
			p.flush(false)
			p.depth++
			p.i++
		case c == ')':
			p.endWord()
			p.flush(false)
			if nested && p.depth == 0 {
				return p.i, len(p.pending) == 0
			}
			if p.depth > 0 {
				p.depth--
			}
			p.i++
		case c == '<' || c == '>':
			if !p.readRedirect(c) {
				return 0, false
			}
		default:
			p.addByte(c)
			p.i++
		}
	}
	if nested {
		return 0, false
	}
	p.endWord()
	p.flush(false)
	return len(p.s), len(p.pending) == 0
}

func (p *uParser) peek(off int) byte {
	if p.i+off < len(p.s) {
		return p.s[p.i+off]
	}
	return 0
}

func (p *uParser) addByte(c byte) {
	p.word.WriteByte(c)
	p.inWord = true
}

func (p *uParser) endWord() {
	if !p.inWord {
		return
	}
	w := uWord{text: p.word.String(), dynamic: p.dyn, procSub: p.proc}
	p.word.Reset()
	p.inWord, p.dyn, p.proc = false, false, false
	switch p.redir {
	case "in":
		if w.procSub {
			p.cur.stdin = "procsub"
		} else {
			p.cur.stdin = "file"
		}
	case "herestring":
		p.cur.stdin, p.cur.stdinText, p.cur.stdinDynamic = "herestring", w.text, w.dynamic
	case "out":
	default:
		p.cur.words = append(p.cur.words, w)
	}
	p.redir = ""
}

func (p *uParser) flush(nextPipe bool) {
	if len(p.cur.words) > 0 || p.cur.stdin != "" {
		p.cur.pipedIn = p.pipe
		p.out.segs = append(p.out.segs, p.cur)
	}
	p.cur = &uSeg{}
	p.pipe = nextPipe
}

// readDouble reads a double-quoted run up to term (0: the end of input, for
// an expanding heredoc body).
func (p *uParser) readDouble(term byte) bool {
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case term != 0 && c == term:
			p.i++
			return true
		case c == '\\' && p.i+1 < len(p.s) && strings.IndexByte("$`\"\\\n", p.s[p.i+1]) >= 0:
			if p.s[p.i+1] != '\n' {
				p.word.WriteByte(p.s[p.i+1])
			}
			p.i += 2
		case c == '$':
			if !p.readDollar() {
				return false
			}
		case c == '`':
			if !p.readBacktick() {
				return false
			}
		default:
			p.word.WriteByte(c)
			p.i++
		}
	}
	return term == 0
}

func (p *uParser) readDollar() bool {
	next := p.peek(1)
	switch {
	case next == '(':
		p.inWord, p.dyn = true, true
		return p.readNested(p.i+2, false, false)
	case next == '{':
		end := braceEnd(p.s, p.i+2)
		if end < 0 {
			return false
		}
		inner := p.s[p.i+2 : end]
		if strings.HasPrefix(inner, "!") {
			// `${!name}` reads whichever variable name holds.
			p.out.vars[indirectExpansion] = true
		}
		name := strings.TrimPrefix(inner, "!")
		n := 0
		for n < len(name) && (isNameByte(name[n]) || (name[n] >= '0' && name[n] <= '9')) {
			n++
		}
		p.out.vars[name[:n]] = true
		if strings.Contains(inner, "$") || strings.Contains(inner, "`") {
			child, ok := parseUnattendedScript(inner)
			if !ok {
				return false
			}
			p.out.children = append(p.out.children, child)
		}
		p.inWord, p.dyn = true, true
		p.i = end + 1
		return true
	case next == '\'':
		j := p.i + 2
		for j < len(p.s) && p.s[j] != '\'' {
			if p.s[j] == '\\' {
				j++
			}
			j++
		}
		if j >= len(p.s) {
			return false
		}
		p.word.WriteString(p.s[p.i+2 : j])
		p.inWord, p.dyn = true, true
		p.i = j + 1
		return true
	case next == '"':
		p.inWord = true
		p.i += 2
		return p.readDouble('"')
	case isNameByte(next):
		j := p.i + 1
		for j < len(p.s) && (isNameByte(p.s[j]) || (p.s[j] >= '0' && p.s[j] <= '9')) {
			j++
		}
		p.out.vars[p.s[p.i+1:j]] = true
		p.inWord, p.dyn = true, true
		p.i = j
		return true
	case next != 0 && strings.IndexByte("0123456789@*#?$!-", next) >= 0:
		p.inWord, p.dyn = true, true
		p.i += 2
		return true
	}
	p.addByte('$')
	p.i++
	return true
}

func (p *uParser) readBacktick() bool {
	var inner strings.Builder
	j := p.i + 1
	for j < len(p.s) && p.s[j] != '`' {
		if p.s[j] == '\\' && j+1 < len(p.s) && strings.IndexByte("`$\\", p.s[j+1]) >= 0 {
			j++
		}
		inner.WriteByte(p.s[j])
		j++
	}
	if j >= len(p.s) {
		return false
	}
	child, ok := parseUnattendedScript(inner.String())
	if !ok {
		return false
	}
	p.out.children = append(p.out.children, child)
	p.inWord, p.dyn = true, true
	p.i = j + 1
	return true
}

// readNested parses a `$(…)`-style region starting at start up to its `)`
// and records it as a child; piped marks a `>(…)` whose first command reads
// the outer output, dataOnly an array or arithmetic body. The parser resumes
// after the `)`.
func (p *uParser) readNested(start int, piped, dataOnly bool) bool {
	sub := newUParser(p.s)
	sub.i = start
	sub.pipe = piped
	end, ok := sub.run(true)
	if !ok {
		return false
	}
	sub.out.dataOnly = dataOnly
	p.out.children = append(p.out.children, sub.out)
	p.i = end + 1
	return true
}

func (p *uParser) readRedirect(c byte) bool {
	if p.inWord && isAllDigits(p.word.String()) && !p.dyn {
		p.word.Reset()
		p.inWord = false // a file descriptor number, as in 2>&1
	}
	p.endWord()
	if p.peek(1) == '(' {
		// `<(…)` / `>(…)`: a path argument whose content a command produces.
		if !p.readNested(p.i+2, c == '>', false) {
			return false
		}
		p.inWord, p.proc = true, true
		p.word.WriteString("/dev/fd/63")
		return true
	}
	if c == '<' && p.peek(1) == '<' {
		if p.peek(2) == '<' {
			p.redir = "herestring"
			p.i += 3
			return true
		}
		return p.readHeredocMarker()
	}
	p.i++
	switch p.peek(0) {
	case '>', '&', '|':
		p.i++
	}
	if c == '<' {
		p.redir = "in"
	} else {
		p.redir = "out"
	}
	return true
}

func (p *uParser) readHeredocMarker() bool {
	p.i += 2
	h := pendingHeredoc{seg: p.cur}
	if p.peek(0) == '-' {
		h.dash = true
		p.i++
	}
	for p.peek(0) == ' ' || p.peek(0) == '\t' {
		p.i++
	}
	var delim strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c == '\'' || c == '"' {
			end := strings.IndexByte(p.s[p.i+1:], c)
			if end < 0 {
				return false
			}
			delim.WriteString(p.s[p.i+1 : p.i+1+end])
			h.quoted = true
			p.i += end + 2
			continue
		}
		if c == '\\' && p.i+1 < len(p.s) {
			delim.WriteByte(p.s[p.i+1])
			h.quoted = true
			p.i += 2
			continue
		}
		if strings.IndexByte(" \t\r\n;&|()<>", c) >= 0 {
			break
		}
		delim.WriteByte(c)
		p.i++
	}
	h.delim = delim.String()
	if h.delim == "" {
		return false
	}
	p.pending = append(p.pending, h)
	return true
}

// readHeredocBodies consumes the bodies of the heredocs opened on the line
// that just ended. An unquoted body expands, so its substitutions are parsed
// as commands too.
func (p *uParser) readHeredocBodies() bool {
	for _, h := range p.pending {
		var body []string
		found := false
		for p.i <= len(p.s) {
			end := strings.IndexByte(p.s[p.i:], '\n')
			line := p.s[p.i:]
			next := len(p.s) + 1
			if end >= 0 {
				line, next = p.s[p.i:p.i+end], p.i+end+1
			}
			p.i = next
			cmp := line
			if h.dash {
				cmp = strings.TrimLeft(cmp, "\t")
			}
			if cmp == h.delim {
				found = true
				break
			}
			body = append(body, line)
			if end < 0 {
				break
			}
		}
		if !found {
			return false
		}
		if p.i > len(p.s) {
			p.i = len(p.s)
		}
		text := strings.Join(body, "\n")
		h.seg.stdin, h.seg.stdinText = "heredoc", text
		if !h.quoted && strings.ContainsAny(text, "$`") {
			h.seg.stdinDynamic = true
			sub := newUParser(text)
			if !sub.readDouble(0) {
				return false
			}
			p.out.children = append(p.out.children, sub.out)
		}
	}
	p.pending = nil
	return true
}

// braceEnd returns the index of the `}` closing a `${` whose body starts at
// start, or -1.
func braceEnd(s string, start int) int {
	depth := 0
	var quote byte
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			}
		case c == '\\':
			i++
		case quote == '"':
			if c == '"' {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '{':
			depth++
		case c == '}':
			if depth == 0 {
				return i
			}
			depth--
		}
	}
	return -1
}

func isNameByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
