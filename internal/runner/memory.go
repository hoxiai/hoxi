package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

const maxSessionConstraints = 48

var (
	memoryWriteMu       sync.Mutex
	volatileUpdatedLine = regexp.MustCompile(`(?m)^Updated:\s*[^\r\n]+\r?\n?`)
	userConstraintRe    = regexp.MustCompile(`(?i)\b(must|must not|mustn't|never|do not|don't|forbidden|forbid|prohibit|constraint|requirement|cannot|can't|should not|shouldn't|only allow|keep .* unchanged|preserve|avoid)\b|严禁|不要|必须|务必|禁止|不能|不可|不得|仅限|只允许|切勿|别改|不准|保持|保留|避免`)
	clauseBreakRe       = regexp.MustCompile(`(?:[.!?](?:\s+|$)|[。！？；;\r\n]+)`)
	verificationCmdRe   = regexp.MustCompile(`(?i)\b(go\s+(?:test|vet|build)|npm\s+(?:--prefix\s+\S+\s+)?(?:test|run\s+(?:test|lint|build|check\S*))|pnpm\s+(?:test|lint|build|check\S*)|yarn\s+(?:test|lint|build)|flutter\s+(?:test|analyze)|pytest|cargo\s+(?:test|check|clippy)|tsc\b|node\s+--test)\b`)
	memorySecretRes     = []*regexp.Regexp{
		regexp.MustCompile(`\b(sk-[a-zA-Z0-9_-]{20,})\b`),
		regexp.MustCompile(`\b(sk-ant-[a-zA-Z0-9_-]{20,})\b`),
		regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9_]{20,})\b`),
		regexp.MustCompile(`\b(AIza[0-9A-Za-z_-]{30,})\b`),
		regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~+/-]{20,}`),
		regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
	}
)

func redactMemorySecrets(text string) string {
	if text == "" {
		return ""
	}
	out := text
	for _, re := range memorySecretRes {
		out = re.ReplaceAllString(out, "[REDACTED_SECRET]")
	}
	return out
}

func extractConstraintClauses(text string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, raw := range clauseBreakRe.Split(text, -1) {
		clean := strings.Join(strings.Fields(raw), " ")
		if clean == "" || !userConstraintRe.MatchString(clean) {
			continue
		}
		clause := excerptAroundMatch(clean, userConstraintRe, 240)
		clause = redactMemorySecrets(clause)
		if clause != "" && !seen[clause] {
			seen[clause] = true
			out = append(out, clause)
		}
	}
	return out
}

func excerptAroundMatch(clean string, re *regexp.Regexp, maxRunes int) string {
	runes := []rune(clean)
	if len(runes) <= maxRunes {
		return clean
	}
	loc := re.FindStringIndex(clean)
	if loc == nil {
		return headTailExcerpt(clean, maxRunes)
	}
	matchStartRune := len([]rune(clean[:loc[0]]))
	// Center window around the constraint match, biased toward keeping the tail after the match keyword.
	winStart := matchStartRune - maxRunes/4
	if winStart < 0 {
		winStart = 0
	}
	winEnd := winStart + maxRunes
	if winEnd > len(runes) {
		winEnd = len(runes)
		winStart = winEnd - maxRunes
		if winStart < 0 {
			winStart = 0
		}
	}
	prefix := ""
	suffix := ""
	if winStart > 0 {
		prefix = "…"
	}
	if winEnd < len(runes) {
		suffix = "…"
	}
	return prefix + string(runes[winStart:winEnd]) + suffix
}

func headTailExcerpt(s string, maxRunes int) string {
	clean := strings.Join(strings.Fields(s), " ")
	runes := []rune(clean)
	if len(runes) <= maxRunes {
		return clean
	}
	headLen := maxRunes / 2
	tailLen := maxRunes - headLen
	return string(runes[:headLen]) + " … " + string(runes[len(runes)-tailLen:])
}

func summarizeUserMessage(txt string, maxRunes int) string {
	excerpt := redactMemorySecrets(headTailExcerpt(txt, maxRunes))
	clauses := extractConstraintClauses(txt)
	var missing []string
	for _, c := range clauses {
		if !strings.Contains(excerpt, c) {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		excerpt += " [Constraints: " + strings.Join(missing, "; ") + "]"
	}
	return excerpt
}

func collectSessionConstraints(turns []MessageTurn) []string {
	var constraints []string
	seen := make(map[string]bool)
	for _, turn := range turns {
		if !strings.EqualFold(strings.TrimSpace(turn.Info.Role), "user") {
			continue
		}
		for _, part := range turn.Parts {
			if part.Type != "text" || strings.TrimSpace(part.Text) == "" {
				continue
			}
			for _, clause := range extractConstraintClauses(part.Text) {
				if seen[clause] {
					continue
				}
				seen[clause] = true
				if len(constraints) >= maxSessionConstraints {
					constraints = constraints[1:]
				}
				constraints = append(constraints, clause)
			}
		}
	}
	return constraints
}

func isMutatingToolName(tool string) bool {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "edit", "write", "multiedit", "apply_patch", "patch",
		"replace_file_content", "multi_replace_file_content",
		"write_to_file", "write_file", "edit_file", "create_file", "sed_file":
		return true
	default:
		return false
	}
}

// stripVolatileMemoryHeader removes dynamic "Updated: <timestamp>" headers so
// injected system/context prefixes remain 100% byte-identical for LLM KV caches.
func stripVolatileMemoryHeader(md string) string {
	return strings.TrimSpace(volatileUpdatedLine.ReplaceAllString(md, ""))
}

// dedupHandoffMemorySection6 collapses Section 6 (All User Messages) when
// runtimeContext(rec) is also being appended in the same handoff prompt, so the
// last 10 user messages are not serialized twice back-to-back.
func dedupHandoffMemorySection6(md string) string {
	const sec6 = "## 6. All User Messages\n"
	const sec7 = "\n## 7. Pending Tasks"
	i := strings.Index(md, sec6)
	if i < 0 {
		return md
	}
	rest := md[i+len(sec6):]
	j := strings.Index(rest, sec7)
	if j < 0 {
		return md
	}
	return md[:i+len(sec6)] + "- Carried in quoted conversation history below.\n" + rest[j:]
}

// projectMemoryDir returns the project root whose .hoxi/memory holds the
// session memory. Isolated sessions run inside <root>/.hoxi/worktrees/<id>, so
// memory must be addressed at the root to survive worktree removal.
func projectMemoryDir(rec SessionRecord) string {
	for _, dir := range []string{rec.ProjectDir, rec.WorktreePath} {
		if root := hoxipaths.ProjectRootForWorktree(dir); root != "" {
			return root
		}
	}
	if dir := strings.TrimSpace(rec.ProjectDir); dir != "" {
		return dir
	}
	return strings.TrimSpace(rec.WorktreePath)
}

// memoryCandidates lists every memory file location to consult, current
// (.hoxi/memory) first, then the worktree copy, then the pre-migration
// .opencode/memory location so existing sessions keep their context.
func memoryCandidates(rec SessionRecord, rel string) []string {
	var paths []string
	seen := make(map[string]bool)
	add := func(root string) {
		root = strings.TrimSpace(root)
		if root == "" {
			return
		}
		for _, base := range []string{hoxipaths.ProjectMemoryDir(root), hoxipaths.LegacyProjectMemoryDir(root)} {
			p := filepath.Join(base, rel)
			if seen[p] {
				continue
			}
			seen[p] = true
			paths = append(paths, p)
		}
	}
	add(projectMemoryDir(rec))
	add(rec.WorktreePath)
	add(rec.ProjectDir)
	return paths
}

// runtimeMemoryContext returns the frozen 8-dimension <project_memory> and
// <session_handoff_memory> blocks for native CLI runtimes on their initial turn
// or after a runtime switch (when ExternalID == "" or ContextPending is true).
func runtimeMemoryContext(rec SessionRecord) string {
	if rec.ExternalID != "" && !rec.ContextPending {
		return ""
	}
	if projectMemoryDir(rec) == "" {
		return ""
	}

	projMem := readFirstNonEmptyFile(memoryCandidates(rec, "project.md")...)
	sessMem := readFirstNonEmptyFile(memoryCandidates(rec, filepath.Join("sessions", rec.ID+".md"))...)
	if rec.ExternalID != "" && sessMem == "" {
		sessMem = readFirstNonEmptyFile(memoryCandidates(rec, filepath.Join("sessions", rec.ExternalID+".md"))...)
	}
	if sessMem == "" && len(rec.Turns) > 0 {
		sessMem = buildEightDimSessionMarkdown(rec)
	}

	projClean := stripVolatileMemoryHeader(projMem)
	sessClean := stripVolatileMemoryHeader(sessMem)
	if rec.ContextPending && len(rec.Turns) > 0 && sessClean != "" {
		sessClean = dedupHandoffMemorySection6(sessClean)
	}
	if projClean == "" && sessClean == "" {
		return ""
	}

	var b strings.Builder
	if projClean != "" {
		b.WriteString("<project_memory>\n")
		b.WriteString(projClean)
		b.WriteString("\n</project_memory>\n\n")
	}
	if sessClean != "" {
		b.WriteString("<session_handoff_memory>\n")
		b.WriteString(sessClean)
		b.WriteString("\n</session_handoff_memory>\n\n")
	}
	return b.String()
}

// syncSessionMemoryFile synthesizes and atomically writes the 8-dimension
// session memory (<project>/.hoxi/memory/sessions/<sessionID>.md) and mirrors
// the project's existing project.md into the session's worktree when missing.
func (s *Service) syncSessionMemoryFile(sessionID string) {
	rec, ok := s.store.Get(sessionID)
	if !ok || len(rec.Turns) == 0 {
		return
	}
	targetRoot := projectMemoryDir(rec)
	if targetRoot == "" {
		return
	}
	if st, err := os.Stat(targetRoot); err != nil || !st.IsDir() {
		return
	}

	// Memory lives at the project root, never inside the removable worktree and
	// never under .opencode (creating that directory makes OpenCode install its
	// plugin bundle into the user's project).
	sessionsDir := filepath.Join(hoxipaths.ProjectMemoryDir(targetRoot), "sessions")
	projFile := hoxipaths.ProjectMemoryFile(targetRoot)

	memoryWriteMu.Lock()
	defer memoryWriteMu.Unlock()

	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		return
	}

	// Mirror an existing project.md into a project that has none, falling back
	// to a pre-migration .opencode copy. No placeholder is ever seeded: project
	// memory is injected into every turn, so boilerplate would cost tokens
	// without adding project knowledge.
	if _, err := os.Stat(projFile); err != nil {
		if raw, readErr := os.ReadFile(hoxipaths.LegacyProjectMemoryFile(targetRoot)); readErr == nil && len(strings.TrimSpace(string(raw))) > 0 {
			_ = writeFileAtomic(projFile, raw)
		}
	}

	md := buildEightDimSessionMarkdown(rec)
	if strings.TrimSpace(md) == "" {
		return
	}
	_ = writeFileAtomic(filepath.Join(sessionsDir, rec.ID+".md"), []byte(md))
}

func buildEightDimSessionMarkdown(rec SessionRecord) string {
	var (
		userMessages    []string
		userConstraints []string
		seenConstraints = make(map[string]bool)
		filesChanged    []string
		seenChanged     = make(map[string]bool)
		filesInspected  []string
		seenInspected   = make(map[string]bool)
		errorsObserved  []string
		verifications   []string
		pendingTodos    []string
		lastAssistant   string
		toolCount       int
	)

	addConstraint := func(clause string) {
		c := strings.TrimSpace(clause)
		if c == "" || seenConstraints[c] {
			return
		}
		seenConstraints[c] = true
		if len(userConstraints) >= maxSessionConstraints {
			userConstraints = userConstraints[1:]
		}
		userConstraints = append(userConstraints, c)
	}

	addFile := func(raw string, mutated bool) {
		p := strings.TrimSpace(raw)
		if p == "" {
			return
		}
		if mutated {
			if !seenChanged[p] && len(filesChanged) < 24 {
				seenChanged[p] = true
				filesChanged = append(filesChanged, p)
			}
			return
		}
		if !seenChanged[p] && !seenInspected[p] && len(filesInspected) < 24 {
			seenInspected[p] = true
			filesInspected = append(filesInspected, p)
		}
	}

	for _, turn := range rec.Turns {
		role := strings.ToLower(strings.TrimSpace(turn.Info.Role))
		for _, part := range turn.Parts {
			switch part.Type {
			case "text":
				txt := strings.TrimSpace(part.Text)
				if txt == "" {
					continue
				}
				if role == "user" {
					excerpt := summarizeUserMessage(txt, 260)
					userMessages = append(userMessages, excerpt)
					clauses := extractConstraintClauses(txt)
					for _, clause := range clauses {
						addConstraint(clause)
					}
					if len(clauses) == 0 && userConstraintRe.MatchString(txt) && len(userMessages) > 1 {
						addConstraint(excerpt)
					}
				} else if role == "assistant" {
					lastAssistant = redactMemorySecrets(singleLineExcerpt(txt, 320))
				}
			case "tool":
				if part.State == nil {
					continue
				}
				toolCount++
				mutated := isMutatingToolName(part.Tool)
				for _, key := range []string{"filePath", "path", "file", "TargetFile", "AbsolutePath"} {
					if v, ok := part.State.Input[key].(string); ok {
						addFile(v, mutated)
					}
				}
				if cmd, ok := part.State.Input["command"].(string); ok && verificationCmdRe.MatchString(cmd) {
					status := firstNonEmptyStr(part.State.Status, "completed")
					entry := fmt.Sprintf("`%s` (%s)", singleLineExcerpt(cmd, 120), status)
					if len(verifications) >= 6 {
						verifications = verifications[1:]
					}
					verifications = append(verifications, entry)
				}
				if strings.EqualFold(part.Tool, "todowrite") {
					if rawTodos, ok := part.State.Input["todos"].([]interface{}); ok {
						pendingTodos = pendingTodos[:0]
						for _, item := range rawTodos {
							m, _ := item.(map[string]interface{})
							if m == nil {
								continue
							}
							content, _ := m["content"].(string)
							status, _ := m["status"].(string)
							if strings.TrimSpace(content) != "" {
								pendingTodos = append(pendingTodos, fmt.Sprintf("[%s] %s", firstNonEmptyStr(status, "pending"), redactMemorySecrets(strings.TrimSpace(content))))
							}
						}
					}
				}
				if part.State.Status == "error" && strings.TrimSpace(part.State.Error) != "" && len(errorsObserved) < 8 {
					errorsObserved = append(errorsObserved, fmt.Sprintf("%s: %s", part.Tool, redactMemorySecrets(singleLineExcerpt(part.State.Error, 180))))
				}
			}
		}
	}

	if len(userMessages) == 0 && lastAssistant == "" && toolCount == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("# Hoxi Session Memory (8-Dimension Compaction)\n")
	b.WriteString(fmt.Sprintf("Session: `%s`\n", rec.ID))
	b.WriteString(fmt.Sprintf("Updated: %s\n\n", time.Now().UTC().Format(time.RFC3339)))

	b.WriteString("## 1. Primary Request and Intent\n")
	if len(userMessages) > 0 {
		b.WriteString("- Initial Request: " + userMessages[0] + "\n")
		if len(userMessages) > 1 {
			b.WriteString("- Latest Request: " + userMessages[len(userMessages)-1] + "\n")
		}
	} else {
		b.WriteString("- Active engineering session in " + firstNonEmptyStr(rec.Title, rec.ID) + "\n")
	}
	for _, c := range userConstraints {
		if len(userMessages) > 0 && (c == userMessages[0] || c == userMessages[len(userMessages)-1]) {
			continue
		}
		b.WriteString("- User Constraint: " + c + "\n")
	}
	b.WriteByte('\n')

	b.WriteString("## 2. Key Technical Concepts\n")
	b.WriteString(fmt.Sprintf("- Runtime: `%s`\n", firstNonEmptyStr(rec.Runtime, "opencode")))
	if rec.ModelID != "" {
		b.WriteString(fmt.Sprintf("- Model: `%s`\n", rec.ModelID))
	}
	if rec.Branch != "" {
		b.WriteString(fmt.Sprintf("- Branch: `%s` (%s)\n", rec.Branch, firstNonEmptyStr(rec.SandboxMode, "local")))
	}
	b.WriteByte('\n')

	b.WriteString("## 3. Files and Code Sections\n")
	totalFiles := 0
	for _, f := range filesChanged {
		b.WriteString("- `" + f + "` (modified)\n")
		totalFiles++
	}
	for _, f := range filesInspected {
		if seenChanged[f] || totalFiles >= 24 {
			continue
		}
		b.WriteString("- `" + f + "`\n")
		totalFiles++
	}
	if totalFiles == 0 {
		b.WriteString("- No explicit file paths recorded yet.\n")
	}
	b.WriteByte('\n')

	b.WriteString("## 4. Errors and Fixes\n")
	if len(errorsObserved) > 0 {
		for _, e := range errorsObserved {
			b.WriteString("- " + e + "\n")
		}
	} else {
		b.WriteString("- No unresolved tool errors recorded in this session.\n")
	}
	b.WriteByte('\n')

	b.WriteString("## 5. Problem Solving\n")
	b.WriteString(fmt.Sprintf("- Executed %d tool invocation(s) across %d conversation turn(s).\n", toolCount, len(rec.Turns)))
	for _, v := range verifications {
		b.WriteString("- Verification: " + v + "\n")
	}
	if lastAssistant != "" {
		b.WriteString("- Latest Summary: " + lastAssistant + "\n")
	}
	b.WriteByte('\n')

	b.WriteString("## 6. All User Messages\n")
	if len(userMessages) > 0 {
		start := 0
		if len(userMessages) > 10 {
			start = len(userMessages) - 10
		}
		for idx, msg := range userMessages[start:] {
			b.WriteString(fmt.Sprintf("%d. %s\n", start+idx+1, msg))
		}
	} else {
		b.WriteString("- None\n")
	}
	b.WriteByte('\n')

	b.WriteString("## 7. Pending Tasks\n")
	if len(pendingTodos) > 0 {
		for _, td := range pendingTodos {
			b.WriteString("- " + td + "\n")
		}
	} else {
		b.WriteString("- All tracked tasks completed or none queued.\n")
	}
	b.WriteByte('\n')

	b.WriteString("## 8. Current Work\n")
	if lastAssistant != "" {
		b.WriteString("- " + lastAssistant + "\n")
	} else if len(userMessages) > 0 {
		b.WriteString("- Addressing: " + userMessages[len(userMessages)-1] + "\n")
	} else {
		b.WriteString("- Ready for next instruction.\n")
	}
	return b.String()
}

func singleLineExcerpt(s string, maxRunes int) string {
	clean := strings.Join(strings.Fields(s), " ")
	runes := []rune(clean)
	if len(runes) <= maxRunes {
		return clean
	}
	return string(runes[:maxRunes]) + "…"
}

func readFirstNonEmptyFile(paths ...string) string {
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		raw, err := os.ReadFile(p)
		if err == nil && len(strings.TrimSpace(string(raw))) > 0 {
			return string(raw)
		}
	}
	return ""
}
