package runner

import "strings"

// MergeSessionMemoryMarkdown preserves manual user edits (custom preambles,
// custom sections, and user-added lines within the 8-dimension sections) when
// automatic turn compaction refreshes a session memory file.
func MergeSessionMemoryMarkdown(existing, autoMD string) string {
	return mergeSessionMemoryWithBaseline(existing, "", autoMD)
}

func isDynamicProgressMemoryLine(sectionHeading, trimmedLine, latestSummary string) bool {
	switch sectionHeading {
	case "## 1. Primary Request and Intent":
		return strings.HasPrefix(trimmedLine, "- Latest Request: ")
	case "## 5. Problem Solving":
		return strings.HasPrefix(trimmedLine, "- Executed ") ||
			strings.HasPrefix(trimmedLine, "- Latest Summary: ")
	case "## 8. Current Work":
		return trimmedLine == "- Ready for next instruction." ||
			strings.HasPrefix(trimmedLine, "- Addressing") ||
			(latestSummary != "" && trimmedLine == "- "+latestSummary)
	default:
		return false
	}
}

func mergeSessionMemoryWithBaseline(existing, baselineAuto, autoMD string) string {
	existingNorm := strings.TrimSpace(normalizeMemoryLF(existing))
	autoNorm := strings.TrimSpace(normalizeMemoryLF(autoMD))
	baselineNorm := strings.TrimSpace(normalizeMemoryLF(baselineAuto))
	hasBaseline := baselineNorm != ""

	if existingNorm == "" {
		if hasBaseline {
			return ""
		}
		if autoNorm == "" {
			return ""
		}
		return clampMemoryDocumentBytes("", autoNorm)
	}
	if autoNorm == "" {
		return clampMemoryDocumentBytes(existingNorm, "") + "\n"
	}
	if hasBaseline && stripVolatileMemoryHeader(existingNorm) == stripVolatileMemoryHeader(baselineNorm) {
		return clampMemoryDocumentBytes("", autoNorm)
	}

	idx := strings.Index(existingNorm, autoSessionMemoryHeading)
	if idx < 0 {
		return clampMemoryDocumentBytes(existingNorm, autoNorm)
	}

	preamble := strings.TrimSpace(existingNorm[:idx])
	existingAuto := existingNorm[idx:]

	autoLineSet := make(map[string]bool, 32)
	collectTrimmedLineSet(autoNorm, autoLineSet)

	var (
		baselineLineSet     map[string]bool
		existingAutoLineSet map[string]bool
		existingHeadingSet  map[string]bool
	)
	if hasBaseline {
		baselineLineSet = make(map[string]bool, 32)
		collectTrimmedLineSet(baselineNorm, baselineLineSet)
		existingAutoLineSet = make(map[string]bool, 32)
		collectTrimmedLineSet(existingAuto, existingAutoLineSet)
		existingHeadingSet = make(map[string]bool, 8)
		for _, sec := range func() []markdownSection { _, s := splitMarkdownSections(existingAuto); return s }() {
			existingHeadingSet[sec.heading] = true
		}
	}
	existingLatestSummary := ""
	if !hasBaseline {
		existingLatestSummary = extractLatestSummaryLine(existingAuto)
	}
	autoLatestSummary := extractLatestSummaryLine(autoNorm)

	var (
		customHeaderLines    []string
		customLinesByHeading map[string][]string
		customSections       []markdownSection
		curHeading           string
		curIsEightDim        bool
	)

	for rest := existingAuto; rest != ""; {
		line, tail, hasMore := strings.Cut(rest, "\n")
		if hasMore {
			rest = tail
		} else {
			rest = ""
		}
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "## ") {
			curHeading = t
			curIsEightDim = isEightDimHeading(t)
			if !curIsEightDim {
				customSections = append(customSections, markdownSection{heading: t})
			}
			continue
		}
		if curHeading == "" {
			if t == "" || autoLineSet[t] || isHeaderMetaLine(t) || (hasBaseline && baselineLineSet[t]) {
				continue
			}
			customHeaderLines = append(customHeaderLines, line)
			continue
		}
		if !curIsEightDim {
			sec := &customSections[len(customSections)-1]
			sec.lines = append(sec.lines, line)
			continue
		}
		if t == "" || autoLineSet[t] || isHeaderMetaLine(t) || isAgyBrainMemoryLine(t) {
			continue
		}
		if hasBaseline {
			if baselineLineSet[t] {
				continue
			}
		} else if isVolatileAutoMemoryLine(curHeading, t, existingLatestSummary) {
			continue
		}
		if customLinesByHeading == nil {
			customLinesByHeading = make(map[string][]string, 4)
		}
		customLinesByHeading[curHeading] = append(customLinesByHeading[curHeading], line)
	}

	userDeletedFromBaseline := false
	if hasBaseline {
		for line := range baselineLineSet {
			if !isHeaderMetaLine(line) && !strings.HasPrefix(line, "## ") && !existingAutoLineSet[line] {
				userDeletedFromBaseline = true
				break
			}
		}
		if !userDeletedFromBaseline {
			_, baseSecs := splitMarkdownSections(baselineNorm)
			for _, bs := range baseSecs {
				if !existingHeadingSet[bs.heading] {
					userDeletedFromBaseline = true
					break
				}
			}
		}
	}

	if preamble == "" && len(customHeaderLines) == 0 && len(customLinesByHeading) == 0 && len(customSections) == 0 && !userDeletedFromBaseline {
		return clampMemoryDocumentBytes("", autoNorm)
	}

	autoHeader, autoSections := splitMarkdownSections(autoNorm)
	var keptAutoSections []markdownSection
	for _, sec := range autoSections {
		if hasBaseline && strings.Contains(baselineNorm, sec.heading) && !existingHeadingSet[sec.heading] {
			continue
		}
		var filteredLines []string
		for _, line := range sec.lines {
			t := strings.TrimSpace(line)
			if t == "" {
				continue
			}
			if hasBaseline && baselineLineSet[t] && !existingAutoLineSet[t] {
				continue
			}
			if hasBaseline && sec.heading == "## 8. Current Work" && len(customLinesByHeading[sec.heading]) > 0 {
				if isDynamicProgressMemoryLine(sec.heading, t, autoLatestSummary) && baselineLineSet[t] && !existingAutoLineSet[t] {
					continue
				}
			}
			filteredLines = append(filteredLines, line)
		}
		sec.lines = filteredLines
		keptAutoSections = append(keptAutoSections, sec)
	}

	merged := renderMergedMemoryDoc(preamble, autoHeader, customHeaderLines, keptAutoSections, customLinesByHeading, customSections)
	for len(merged) > MaxMemoryDocumentBytes {
		shrank := false
		for i := len(keptAutoSections) - 1; i >= 0; i-- {
			sec := &keptAutoSections[i]
			if len(sec.lines) > 0 {
				sec.lines = sec.lines[:len(sec.lines)-1]
				if len(sec.lines) == 0 && len(customLinesByHeading[sec.heading]) == 0 {
					keptAutoSections = append(keptAutoSections[:i], keptAutoSections[i+1:]...)
				}
				shrank = true
				break
			}
			if len(customLinesByHeading[sec.heading]) == 0 {
				keptAutoSections = append(keptAutoSections[:i], keptAutoSections[i+1:]...)
				shrank = true
				break
			}
		}
		if !shrank && len(autoHeader) > 1 {
			autoHeader = autoHeader[:1]
			shrank = true
		}
		if !shrank {
			break
		}
		merged = renderMergedMemoryDoc(preamble, autoHeader, customHeaderLines, keptAutoSections, customLinesByHeading, customSections)
	}
	if len(merged) > MaxMemoryDocumentBytes {
		if len(existingNorm)+1 <= MaxMemoryDocumentBytes {
			return existingNorm + "\n"
		}
		return truncateUTF8Bytes(merged, MaxMemoryDocumentBytes-1) + "\n"
	}
	return merged
}

func renderMergedMemoryDoc(
	preamble string,
	autoHeader, customHeaderLines []string,
	keptAutoSections []markdownSection,
	customLinesByHeading map[string][]string,
	customSections []markdownSection,
) string {
	var b strings.Builder
	if preamble != "" {
		b.WriteString(preamble)
		b.WriteString("\n\n")
	}
	for _, line := range autoHeader {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	for _, line := range customHeaderLines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if len(customHeaderLines) > 0 {
		b.WriteByte('\n')
	}
	for i, sec := range keptAutoSections {
		b.WriteString(sec.heading)
		b.WriteByte('\n')
		lines := sec.lines
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		for _, line := range lines {
			b.WriteString(line)
			b.WriteByte('\n')
		}
		for _, extra := range customLinesByHeading[sec.heading] {
			b.WriteString(extra)
			b.WriteByte('\n')
		}
		if i < len(keptAutoSections)-1 || len(customSections) > 0 {
			b.WriteByte('\n')
		}
	}
	for i, sec := range customSections {
		b.WriteString(sec.heading)
		b.WriteByte('\n')
		lines := sec.lines
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		for _, line := range lines {
			b.WriteString(line)
			b.WriteByte('\n')
		}
		if i < len(customSections)-1 {
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func clampMemoryDocumentBytes(customBlock, autoNorm string) string {
	if autoNorm == "" {
		return truncateUTF8Bytes(customBlock, MaxMemoryDocumentBytes-1)
	}
	if customBlock == "" {
		if len(autoNorm)+1 <= MaxMemoryDocumentBytes {
			return autoNorm + "\n"
		}
		return truncateUTF8Bytes(autoNorm, MaxMemoryDocumentBytes-1) + "\n"
	}
	combined := customBlock + "\n\n" + autoNorm + "\n"
	if len(combined) <= MaxMemoryDocumentBytes {
		return combined
	}
	if len(customBlock)+1 >= MaxMemoryDocumentBytes {
		return truncateUTF8Bytes(customBlock, MaxMemoryDocumentBytes-1) + "\n"
	}
	budgetForAuto := MaxMemoryDocumentBytes - len(customBlock) - 3
	if budgetForAuto <= 32 {
		return customBlock + "\n"
	}
	trimmedAuto := strings.TrimSpace(truncateUTF8Bytes(autoNorm, budgetForAuto))
	if trimmedAuto == "" {
		return customBlock + "\n"
	}
	return customBlock + "\n\n" + trimmedAuto + "\n"
}
