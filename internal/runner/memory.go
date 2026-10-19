package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	memoryWriteMu      sync.Mutex
	volatileUpdatedLine = regexp.MustCompile(`(?m)^Updated:\s*[^\r\n]+\r?\n?`)
)

const defaultProjectMemoryTemplate = `# Hoxi Project Memory (8-Dimension Structured Context)

## 1. Primary Request and Intent
- Build and maintain the project workspace with high engineering discipline.

## 2. Key Technical Concepts
- Follow repository architecture boundaries, type safety, and verification gates.

## 3. Files and Code Sections
- Core files are tracked per session in .opencode/memory/sessions/.

## 4. Errors and Fixes
- Document root causes and verified fixes as turns complete.

## 5. Problem Solving
- Prefer minimal, root-cause changes backed by automated tests.

## 6. All User Messages
- User intent history is indexed per session.

## 7. Pending Tasks
- Check active session memory for in-progress and pending items.

## 8. Current Work
- Initialized by Hoxi multi-runtime memory engine.
`

// stripVolatileMemoryHeader removes dynamic "Updated: <timestamp>" headers so
// injected system/context prefixes remain 100% byte-identical for LLM KV caches.
func stripVolatileMemoryHeader(md string) string {
	return strings.TrimSpace(volatileUpdatedLine.ReplaceAllString(md, ""))
}

// runtimeMemoryContext returns the frozen 8-dimension <project_memory> and
// <session_handoff_memory> blocks for native CLI runtimes on their initial turn
// or after a runtime switch (when ExternalID == "" or ContextPending is true).
func runtimeMemoryContext(rec SessionRecord) string {
	if rec.ExternalID != "" && !rec.ContextPending {
		return ""
	}
	workDir := strings.TrimSpace(rec.WorktreePath)
	projDir := strings.TrimSpace(rec.ProjectDir)
	if workDir == "" && projDir == "" {
		return ""
	}

	projMem := readFirstNonEmptyFile(
		filepath.Join(workDir, ".opencode", "memory", "project.md"),
		filepath.Join(projDir, ".opencode", "memory", "project.md"),
	)
	sessMem := readFirstNonEmptyFile(
		filepath.Join(workDir, ".opencode", "memory", "sessions", rec.ID+".md"),
		filepath.Join(projDir, ".opencode", "memory", "sessions", rec.ID+".md"),
	)
	if rec.ExternalID != "" && sessMem == "" {
		sessMem = readFirstNonEmptyFile(
			filepath.Join(workDir, ".opencode", "memory", "sessions", rec.ExternalID+".md"),
			filepath.Join(projDir, ".opencode", "memory", "sessions", rec.ExternalID+".md"),
		)
	}

	projClean := stripVolatileMemoryHeader(projMem)
	sessClean := stripVolatileMemoryHeader(sessMem)
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
// session memory (.opencode/memory/sessions/<sessionID>.md) and ensures
// .opencode/memory/project.md exists in the session's active directory.
func (s *Service) syncSessionMemoryFile(sessionID string) {
	rec, ok := s.store.Get(sessionID)
	if !ok || len(rec.Turns) == 0 {
		return
	}
	targetDir := strings.TrimSpace(rec.WorktreePath)
	if targetDir == "" {
		targetDir = strings.TrimSpace(rec.ProjectDir)
	}
	if targetDir == "" {
		return
	}
	if st, err := os.Stat(targetDir); err != nil || !st.IsDir() {
		return
	}

	// If OpenCode plugin already wrote a richer session file under ExternalID, mirror or merge it.
	sessionsDir := filepath.Join(targetDir, ".opencode", "memory", "sessions")
	projFile := filepath.Join(targetDir, ".opencode", "memory", "project.md")

	memoryWriteMu.Lock()
	defer memoryWriteMu.Unlock()

	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		return
	}

	// Ensure project.md exists in targetDir (seed from ProjectDir if available).
	if _, err := os.Stat(projFile); err != nil {
		seed := defaultProjectMemoryTemplate
		if rec.ProjectDir != "" && rec.ProjectDir != targetDir {
			if raw, readErr := os.ReadFile(filepath.Join(rec.ProjectDir, ".opencode", "memory", "project.md")); readErr == nil && len(strings.TrimSpace(string(raw))) > 0 {
				seed = string(raw)
			}
		}
		_ = writeFileAtomic(projFile, []byte(seed))
	}

	md := buildEightDimSessionMarkdown(rec)
	if strings.TrimSpace(md) == "" {
		return
	}
	_ = writeFileAtomic(filepath.Join(sessionsDir, rec.ID+".md"), []byte(md))
}

func buildEightDimSessionMarkdown(rec SessionRecord) string {
	var (
		userMessages  []string
		filesTouched  []string
		seenFiles     = make(map[string]bool)
		errorsObserved []string
		pendingTodos  []string
		lastAssistant string
		toolCount     int
	)

	addFile := func(raw string) {
		p := strings.TrimSpace(raw)
		if p == "" || seenFiles[p] || len(filesTouched) >= 24 {
			return
		}
		seenFiles[p] = true
		filesTouched = append(filesTouched, p)
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
					userMessages = append(userMessages, singleLineExcerpt(txt, 220))
				} else if role == "assistant" {
					lastAssistant = singleLineExcerpt(txt, 320)
				}
			case "tool":
				if part.State == nil {
					continue
				}
				toolCount++
				for _, key := range []string{"filePath", "path", "file", "TargetFile", "AbsolutePath"} {
					if v, ok := part.State.Input[key].(string); ok {
						addFile(v)
					}
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
								pendingTodos = append(pendingTodos, fmt.Sprintf("[%s] %s", firstNonEmptyStr(status, "pending"), strings.TrimSpace(content)))
							}
						}
					}
				}
				if part.State.Status == "error" && strings.TrimSpace(part.State.Error) != "" && len(errorsObserved) < 8 {
					errorsObserved = append(errorsObserved, fmt.Sprintf("%s: %s", part.Tool, singleLineExcerpt(part.State.Error, 180)))
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
	if len(filesTouched) > 0 {
		for _, f := range filesTouched {
			b.WriteString("- `" + f + "`\n")
		}
	} else {
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

