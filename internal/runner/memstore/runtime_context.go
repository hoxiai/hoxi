package memstore

import (
	"path/filepath"
	"strings"
)

// RuntimeMemoryBlocks separates shared project guidance from the current
// conversation checkpoint so a changing transcript can travel with user input.
func RuntimeMemoryBlocks(rec SessionRecord) (project, handoff string) {
	if rec.ExternalID != "" && !rec.ContextPending {
		return "", ""
	}
	return memoryBlocks(rec, true)
}

// NativeMemoryBlocks omits machine-generated progress already present in canonical
// history, except when handing off a session or recovering an interrupted turn.
func NativeMemoryBlocks(rec SessionRecord, recovering bool) (project, session string) {
	return memoryBlocks(rec, rec.ContextPending || recovering)
}

func memoryBlocks(rec SessionRecord, includeProgress bool) (project, handoff string) {
	if projectMemoryDir(rec) == "" {
		return "", ""
	}

	projMem := readFirstNonEmptyFile(memoryCandidates(rec, "project.md")...)
	// Read the editable document and its generated baseline from the same refresh.
	memoryWriteMu.Lock()
	sessMem := readFirstNonEmptyFile(memoryCandidates(rec, filepath.Join("sessions", rec.ID+".md"))...)
	if rec.ExternalID != "" && sessMem == "" {
		sessMem = readFirstNonEmptyFile(memoryCandidates(rec, filepath.Join("sessions", rec.ExternalID+".md"))...)
	}
	if !includeProgress && sessMem != "" {
		root := projectMemoryDir(rec)
		baseline := sessionMemoryBaselineFile(root, rec.ID)
		if isMemoryPathWithinRoot(root, baseline) {
			sessMem = nativeMemoryNotes(sessMem, readFirstNonEmptyFile(baseline))
		}
	}
	memoryWriteMu.Unlock()
	if includeProgress && sessMem == "" && len(rec.Turns) > 0 {
		sessMem = buildEightDimSessionMarkdown(rec)
	}

	projClean := stripVolatileMemoryHeader(projMem)
	sessClean := sanitizeSessionMemoryMarkdown(stripVolatileMemoryHeader(sessMem))
	if includeProgress && (rec.ContextPending || strings.TrimSpace(rec.ExternalID) == "") && len(rec.Turns) > 0 && sessClean != "" {
		sessClean = dedupHandoffMemorySection6(sessClean)
	}
	if projClean != "" {
		project = "<project_memory>\n" + projClean + "\n</project_memory>\n\n"
	}
	if sessClean != "" {
		handoff = "<session_handoff_memory>\n" + sessClean + "\n</session_handoff_memory>\n\n"
	}
	return project, handoff
}

// Legacy documents without a baseline are kept intact. Compare within each
// Markdown section so an identical line in a different section remains a note.
func nativeMemoryNotes(document, baseline string) string {
	document = stripVolatileMemoryHeader(normalizeMemoryLF(document))
	baseline = stripVolatileMemoryHeader(normalizeMemoryLF(baseline))
	if document == "" || !strings.HasPrefix(baseline, autoSessionMemoryHeading+"\n") {
		return document
	}
	if document == baseline {
		return ""
	}
	// The merger preserves a custom document before the generated title. Its
	// last Markdown section must not absorb the following automatic header.
	var parts []string
	if preamble, body, ok := strings.Cut(document, autoSessionMemoryHeading); ok {
		if authored := strings.TrimSpace(preamble); authored != "" {
			parts = append(parts, authored)
		}
		document = autoSessionMemoryHeading + body
	}
	header, sections := splitMarkdownSections(document)
	baseHeader, baseSections := splitMarkdownSections(baseline)
	generated := map[string][]string{"": baseHeader}
	for _, sec := range baseSections {
		generated[sec.heading] = sec.lines
	}
	add := func(heading string, lines []string) {
		base, exists := generated[heading]
		if !exists {
			parts = append(parts, strings.TrimSpace(heading+"\n"+strings.Join(lines, "\n")))
			return
		}
		known := make(map[string]bool, len(base))
		for _, line := range base {
			known[line] = true
		}
		var notes []string
		for _, line := range lines {
			if !known[line] {
				notes = append(notes, line)
			}
		}
		if body := strings.Trim(strings.Join(notes, "\n"), "\n"); strings.TrimSpace(body) != "" {
			parts = append(parts, strings.Trim(heading+"\n"+body, "\n"))
		}
	}
	add("", header)
	for _, sec := range sections {
		add(sec.heading, sec.lines)
	}
	return strings.Join(parts, "\n\n")
}
