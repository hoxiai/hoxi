package attachment

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ApplySpreadsheetEdits reads an existing spreadsheet (or creates a new one),
// applies cell edits (e.g. "B2": "1580.5") and/or appends rows to sheetName,
// and writes the updated spreadsheet to outPath.
func ApplySpreadsheetEdits(inPath, outPath, sheetName string, cellEdits map[string]string, appendRows [][]string) ([]SpreadsheetSheet, error) {
	if outPath == "" {
		outPath = inPath
	}

	parsedEdits := make(map[int]map[int]string, len(cellEdits))
	for ref, val := range cellEdits {
		rIdx, cIdx, ok := ParseCellRef(ref)
		if !ok {
			return nil, fmt.Errorf("invalid cell reference %q (expected A1-style like B2)", ref)
		}
		if parsedEdits[rIdx] == nil {
			parsedEdits[rIdx] = make(map[int]string)
		}
		parsedEdits[rIdx][cIdx] = val
	}

	outExt := strings.ToLower(filepath.Ext(outPath))
	inExt := strings.ToLower(filepath.Ext(inPath))

	if (outExt == ".csv" || outExt == ".tsv") && (inPath == "" || inExt == ".csv" || inExt == ".tsv") {
		inComma, outComma := ',', ','
		if inExt == ".tsv" {
			inComma = '\t'
		}
		if outExt == ".tsv" {
			outComma = '\t'
		}
		return applyDelimitedFileEdits(inPath, outPath, sheetName, inComma, outComma, parsedEdits, appendRows)
	}

	if outExt != ".csv" && outExt != ".tsv" && inPath != "" {
		if _, err := os.Stat(inPath); err == nil {
			sheets, handled, err := applyXLSXZipEdits(inPath, outPath, sheetName, parsedEdits, appendRows)
			if err != nil || handled {
				return sheets, err
			}
		}
	}

	var sheets []SpreadsheetSheet
	if inPath != "" {
		if _, err := os.Stat(inPath); err == nil {
			parsed, parseErr := parseSpreadsheetFileForEdit(inPath)
			if parseErr != nil {
				return nil, parseErr
			}
			sheets = parsed
		}
	}
	if len(sheets) == 0 {
		name := strings.TrimSpace(sheetName)
		if name == "" {
			name = "Sheet1"
		}
		sheets = []SpreadsheetSheet{{Name: name}}
	}

	targetIdx := 0
	if trimmed := strings.TrimSpace(sheetName); trimmed != "" {
		found := -1
		for i, s := range sheets {
			if strings.EqualFold(s.Name, trimmed) {
				found = i
				break
			}
		}
		if found >= 0 {
			targetIdx = found
		} else {
			sheets = append(sheets, SpreadsheetSheet{Name: trimmed})
			targetIdx = len(sheets) - 1
		}
	}

	rows := sheets[targetIdx].Rows
	for _, rIdx := range sortedEditRowKeys(parsedEdits) {
		for len(rows) <= rIdx {
			rows = append(rows, nil)
		}
		for cIdx, val := range parsedEdits[rIdx] {
			for len(rows[rIdx]) <= cIdx {
				rows[rIdx] = append(rows[rIdx], "")
			}
			rows[rIdx][cIdx] = val
		}
	}
	if len(appendRows) > 0 {
		rows = append(rows, appendRows...)
	}
	sheets[targetIdx].Rows = rows
	sheets[targetIdx].TotalRows = len(rows)

	switch outExt {
	case ".csv":
		return sheets, WriteDelimitedFile(outPath, sheets[targetIdx].Rows, ',')
	case ".tsv":
		return sheets, WriteDelimitedFile(outPath, sheets[targetIdx].Rows, '\t')
	default:
		return sheets, WriteXLSXFile(outPath, sheets)
	}
}

func applyDelimitedFileEdits(inPath, outPath, sheetName string, inComma, outComma rune, parsedEdits map[int]map[int]string, appendRows [][]string) ([]SpreadsheetSheet, error) {
	var inFile *os.File
	if inPath != "" {
		f, err := os.Open(inPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		inFile = f
	}
	if inFile != nil {
		defer inFile.Close()
	}

	dir := filepath.Dir(outPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	tmpFile, err := os.CreateTemp(dir, filepath.Base(outPath)+".tmp-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmpFile.Name()
	cleanup := func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
	}

	bw := bufio.NewWriterSize(tmpFile, 256*1024)
	w := csv.NewWriter(bw)
	if outComma != 0 {
		w.Comma = outComma
	}

	maxEditRow := -1
	for rIdx := range parsedEdits {
		if rIdx > maxEditRow {
			maxEditRow = rIdx
		}
	}

	const maxInMemoryPreviewBytes = 16 << 20
	var memRows [][]string
	memBytes := 0
	truncated := false
	rowIdx := 0

	recordRow := func(row []string) error {
		if err := w.Write(row); err != nil {
			return err
		}
		if !truncated {
			rowBytes := 16
			for _, cell := range row {
				rowBytes += len(cell) + 16
			}
			if memBytes+rowBytes <= maxInMemoryPreviewBytes {
				memRows = append(memRows, row)
				memBytes += rowBytes
			} else {
				truncated = true
			}
		}
		rowIdx++
		return nil
	}

	if inFile != nil {
		br := bufio.NewReaderSize(inFile, 256*1024)
		if bom, err := br.Peek(3); err == nil && bytes.Equal(bom, []byte("\xEF\xBB\xBF")) {
			_, _ = br.Discard(3)
		}
		r := csv.NewReader(br)
		if inComma != 0 {
			r.Comma = inComma
		}
		r.FieldsPerRecord = -1
		r.LazyQuotes = true
		for {
			rec, readErr := r.Read()
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				cleanup()
				return nil, readErr
			}
			row := make([]string, len(rec))
			copy(row, rec)
			if colEdits, ok := parsedEdits[rowIdx]; ok {
				for cIdx, val := range colEdits {
					for len(row) <= cIdx {
						row = append(row, "")
					}
					row[cIdx] = val
				}
			}
			if err := recordRow(row); err != nil {
				cleanup()
				return nil, err
			}
		}
		_ = inFile.Close()
		inFile = nil
	}

	for rowIdx <= maxEditRow {
		var row []string
		if colEdits, ok := parsedEdits[rowIdx]; ok {
			for cIdx, val := range colEdits {
				for len(row) <= cIdx {
					row = append(row, "")
				}
				row[cIdx] = val
			}
		}
		if err := recordRow(row); err != nil {
			cleanup()
			return nil, err
		}
	}

	for _, appRow := range appendRows {
		row := make([]string, len(appRow))
		copy(row, appRow)
		if err := recordRow(row); err != nil {
			cleanup()
			return nil, err
		}
	}

	w.Flush()
	if err := w.Error(); err != nil {
		cleanup()
		return nil, err
	}
	if err := bw.Flush(); err != nil {
		cleanup()
		return nil, err
	}
	if err := tmpFile.Sync(); err != nil {
		cleanup()
		return nil, err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpName)
		return nil, err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		_ = os.Remove(tmpName)
		return nil, err
	}
	if err := os.Rename(tmpName, outPath); err != nil {
		_ = os.Remove(tmpName)
		return nil, err
	}

	resolvedName := strings.TrimSpace(sheetName)
	if resolvedName == "" {
		resolvedName = strings.TrimSuffix(filepath.Base(outPath), filepath.Ext(outPath))
	}
	if resolvedName == "" {
		resolvedName = "Sheet1"
	}
	return []SpreadsheetSheet{{
		Name:      resolvedName,
		Rows:      memRows,
		TotalRows: rowIdx,
		Truncated: truncated,
	}}, nil
}

func applyXLSXZipEdits(
	inPath, outPath, sheetName string,
	parsedEdits map[int]map[int]string,
	appendRows [][]string,
) ([]SpreadsheetSheet, bool, error) {
	zr, err := zip.OpenReader(inPath)
	if err != nil {
		return nil, false, nil
	}
	defer zr.Close()

	entries := resolveXLSXSheets(zr)
	if len(entries) == 0 {
		return nil, false, nil
	}

	targetIdx := 0
	if trimmed := strings.TrimSpace(sheetName); trimmed != "" {
		found := -1
		for i, entry := range entries {
			if strings.EqualFold(entry.name, trimmed) {
				found = i
				break
			}
		}
		if found < 0 {
			return nil, false, nil
		}
		targetIdx = found
	}
	targetEntry := entries[targetIdx]

	var sstFile *zip.File
	var sstRaw []byte
	var shared []string
	for _, f := range zr.File {
		if strings.TrimPrefix(f.Name, "/") == "xl/sharedStrings.xml" {
			sstFile = f
			raw, readErr := readZipEntryBytesBounded(f, 64*1024*1024)
			if readErr != nil {
				return nil, false, readErr
			}
			sstRaw = raw
			shared = parseXLSXSharedStrings(f)
			break
		}
	}

	sharedIndex := make(map[string]int, len(shared))
	for i, s := range shared {
		if _, exists := sharedIndex[s]; !exists {
			sharedIndex[s] = i
		}
	}
	var appendedShared []string
	internString := func(s string) int {
		if idx, ok := sharedIndex[s]; ok {
			return idx
		}
		idx := len(shared)
		shared = append(shared, s)
		sharedIndex[s] = idx
		appendedShared = append(appendedShared, s)
		return idx
	}

	wsRaw, err := readZipEntryBytesBounded(targetEntry.file, 64*1024*1024)
	if err != nil {
		return nil, false, err
	}
	patchedWS, ok := patchXLSXWorksheetXML(string(wsRaw), parsedEdits, appendRows, internString, sstFile != nil)
	if !ok {
		return nil, false, nil
	}

	var patchedSST string
	if sstFile != nil && len(appendedShared) > 0 {
		patchedSST = appendXLSXSharedStringsXML(string(sstRaw), len(shared), appendedShared)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, zf := range zr.File {
		w, err := zw.Create(zf.Name)
		if err != nil {
			_ = zw.Close()
			return nil, false, err
		}
		switch {
		case zf == targetEntry.file:
			if _, err := w.Write([]byte(patchedWS)); err != nil {
				_ = zw.Close()
				return nil, false, err
			}
		case sstFile != nil && zf == sstFile && len(appendedShared) > 0:
			if _, err := w.Write([]byte(patchedSST)); err != nil {
				_ = zw.Close()
				return nil, false, err
			}
		default:
			rc, err := zf.Open()
			if err != nil {
				_ = zw.Close()
				return nil, false, err
			}
			_, copyErr := io.Copy(w, rc)
			_ = rc.Close()
			if copyErr != nil {
				_ = zw.Close()
				return nil, false, copyErr
			}
		}
	}
	if err := zw.Close(); err != nil {
		return nil, false, err
	}
	_ = zr.Close()

	if err := writeBytesAtomically(outPath, buf.Bytes()); err != nil {
		return nil, false, err
	}
	sheets, err := parseSpreadsheetFileForEdit(outPath)
	return sheets, true, err
}

func readZipEntryBytesBounded(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("xlsx entry %s exceeds maximum editable size (%d MiB)", f.Name, limit/(1024*1024))
	}
	return data, nil
}

func appendXLSXSharedStringsXML(sstXML string, totalUnique int, added []string) string {
	start, openEnd, closeStart, _, sstPrefix, selfClosing, ok := findXMLElementSpan(sstXML, "sst")
	var extra strings.Builder
	for _, s := range added {
		fmt.Fprintf(&extra, `<%ssi><%st xml:space="preserve">%s</%st></%ssi>`, sstPrefix, sstPrefix, xmlEscapeString(s), sstPrefix, sstPrefix)
	}
	if ok && !selfClosing {
		return sstXML[:closeStart] + extra.String() + sstXML[closeStart:]
	}
	if ok && selfClosing {
		openTag := strings.TrimSuffix(strings.TrimSpace(sstXML[start:openEnd]), "/>") + ">"
		return sstXML[:start] + openTag + extra.String() + "</" + sstPrefix + "sst>" + sstXML[openEnd:]
	}
	var fallback strings.Builder
	fallback.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	fmt.Fprintf(&fallback, `<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="%d" uniqueCount="%d">`, totalUnique, totalUnique)
	fallback.WriteString(extra.String())
	fallback.WriteString(`</sst>`)
	return fallback.String()
}

func patchXLSXWorksheetXML(
	xmlStr string,
	parsedEdits map[int]map[int]string,
	appendRows [][]string,
	internString func(string) int,
	hasSST bool,
) (string, bool) {
	sdStart, sdOpenEnd, sdCloseStart, sdCloseEnd, nsPrefix, sdSelfClosing, ok := findXMLElementSpan(xmlStr, "sheetData")
	if !ok {
		return "", false
	}

	var prefix, inner, suffix string
	if sdSelfClosing {
		openTag := strings.TrimSuffix(strings.TrimSpace(xmlStr[sdStart:sdOpenEnd]), "/>") + ">"
		prefix = xmlStr[:sdStart] + openTag
		inner = ""
		suffix = "</" + nsPrefix + "sheetData>" + xmlStr[sdCloseEnd:]
	} else {
		prefix = xmlStr[:sdOpenEnd]
		inner = xmlStr[sdOpenEnd:sdCloseStart]
		suffix = xmlStr[sdCloseStart:]
	}

	editRows := sortedEditRowKeys(parsedEdits)
	editCursor := 0
	seqRowIdx := 0
	maxRowIdx := -1
	pos := 0
	newRowPrefix := nsPrefix
	seenRowPrefix := false
	var out strings.Builder
	out.Grow(len(inner) + 256)

	for {
		rStart, rOpenEnd, rCloseStart, rCloseEnd, rowPrefix, rSelfClosing, found := nextXMLElement(inner, pos, "row")
		if !found {
			break
		}
		if !seenRowPrefix {
			newRowPrefix = rowPrefix
			seenRowPrefix = true
		}
		out.WriteString(inner[pos:rStart])

		rowOpenTag := inner[rStart:rOpenEnd]
		rowInner := ""
		if !rSelfClosing {
			rowInner = inner[rOpenEnd:rCloseStart]
		}
		rowIdx := resolveXLSXRowElementIndex(rowOpenTag, rowInner, seqRowIdx)
		seqRowIdx = rowIdx + 1

		for editCursor < len(editRows) && editRows[editCursor] < rowIdx {
			er := editRows[editCursor]
			out.WriteString(buildNewXLSXRowXML(er, parsedEdits[er], newRowPrefix, internString, hasSST))
			if er > maxRowIdx {
				maxRowIdx = er
			}
			editCursor++
		}

		if editCursor < len(editRows) && editRows[editCursor] == rowIdx {
			editCursor++
			out.WriteString(patchXLSXRowElementXML(rowIdx, rowOpenTag, rowInner, rowPrefix, rSelfClosing, parsedEdits[rowIdx], internString, hasSST))
		} else {
			out.WriteString(inner[rStart:rCloseEnd])
		}
		if rowIdx > maxRowIdx {
			maxRowIdx = rowIdx
		}
		pos = rCloseEnd
	}
	out.WriteString(inner[pos:])

	for editCursor < len(editRows) {
		er := editRows[editCursor]
		out.WriteString(buildNewXLSXRowXML(er, parsedEdits[er], newRowPrefix, internString, hasSST))
		if er > maxRowIdx {
			maxRowIdx = er
		}
		editCursor++
	}

	for _, appRow := range appendRows {
		maxRowIdx++
		rowMap := make(map[int]string, len(appRow))
		for cIdx, v := range appRow {
			if strings.TrimSpace(v) != "" {
				rowMap[cIdx] = v
			}
		}
		out.WriteString(buildNewXLSXRowXML(maxRowIdx, rowMap, newRowPrefix, internString, hasSST))
	}

	return prefix + out.String() + suffix, true
}

type existingXLSXCell struct {
	rawXML string
	style  string
}

func patchXLSXRowElementXML(
	rowIdx int,
	rowOpenTag, rowInner, rowPrefix string,
	rowSelfClosing bool,
	colEdits map[int]string,
	internString func(string) int,
	hasSST bool,
) string {
	existing := make(map[int]existingXLSXCell)
	colSet := make(map[int]bool, len(colEdits)+4)
	for cIdx := range colEdits {
		colSet[cIdx] = true
	}

	cellPrefix := rowPrefix
	seenCellPrefix := false
	seqColIdx := 0
	pos := 0
	for {
		cStart, cOpenEnd, _, cCloseEnd, cPrefix, _, found := nextXMLElement(rowInner, pos, "c")
		if !found {
			break
		}
		if !seenCellPrefix {
			cellPrefix = cPrefix
			seenCellPrefix = true
		}
		cOpenTag := rowInner[cStart:cOpenEnd]
		cRef := extractXMLAttr(cOpenTag, "r")
		colIdx := excelColIndex(cRef)
		if colIdx < 0 || colIdx > 16384 {
			colIdx = seqColIdx
		}
		seqColIdx = colIdx + 1
		existing[colIdx] = existingXLSXCell{
			rawXML: rowInner[cStart:cCloseEnd],
			style:  extractXMLAttr(cOpenTag, "s"),
		}
		colSet[colIdx] = true
		pos = cCloseEnd
	}

	cols := make([]int, 0, len(colSet))
	for cIdx := range colSet {
		cols = append(cols, cIdx)
	}
	sort.Ints(cols)

	openTag := rowOpenTag
	if rowSelfClosing {
		openTag = strings.TrimSuffix(strings.TrimSpace(rowOpenTag), "/>") + ">"
	}

	var b strings.Builder
	b.WriteString(openTag)
	for _, cIdx := range cols {
		if newVal, edited := colEdits[cIdx]; edited {
			style := existing[cIdx].style
			b.WriteString(buildXLSXCellXML(rowIdx, cIdx, newVal, style, cellPrefix, internString, hasSST))
		} else if prev, ok := existing[cIdx]; ok {
			b.WriteString(prev.rawXML)
		}
	}
	b.WriteString("</" + rowPrefix + "row>")
	return b.String()
}

func buildNewXLSXRowXML(rowIdx int, colEdits map[int]string, nsPrefix string, internString func(string) int, hasSST bool) string {
	cols := make([]int, 0, len(colEdits))
	for cIdx := range colEdits {
		cols = append(cols, cIdx)
	}
	sort.Ints(cols)

	var b strings.Builder
	fmt.Fprintf(&b, `<%srow r="%d">`, nsPrefix, rowIdx+1)
	for _, cIdx := range cols {
		b.WriteString(buildXLSXCellXML(rowIdx, cIdx, colEdits[cIdx], "", nsPrefix, internString, hasSST))
	}
	fmt.Fprintf(&b, `</%srow>`, nsPrefix)
	return b.String()
}

func buildXLSXCellXML(rowIdx, colIdx int, rawVal, style, nsPrefix string, internString func(string) int, hasSST bool) string {
	cellRef := fmt.Sprintf("%s%d", ExcelColName(colIdx), rowIdx+1)
	styleAttr := ""
	if style != "" {
		styleAttr = fmt.Sprintf(` s="%s"`, xmlEscapeString(style))
	}
	val := strings.TrimSpace(rawVal)
	if val == "" {
		if styleAttr != "" {
			return fmt.Sprintf(`<%sc r="%s"%s/>`, nsPrefix, cellRef, styleAttr)
		}
		return ""
	}
	if strings.HasPrefix(val, "=") && len(val) > 1 {
		return fmt.Sprintf(`<%sc r="%s"%s><%sf>%s</%sf></%sc>`, nsPrefix, cellRef, styleAttr, nsPrefix, xmlEscapeString(val[1:]), nsPrefix, nsPrefix)
	}
	if isStrictNumber(val) {
		return fmt.Sprintf(`<%sc r="%s"%s><%sv>%s</%sv></%sc>`, nsPrefix, cellRef, styleAttr, nsPrefix, xmlEscapeString(val), nsPrefix, nsPrefix)
	}
	if strings.EqualFold(val, "TRUE") || strings.EqualFold(val, "FALSE") {
		bVal := "0"
		if strings.EqualFold(val, "TRUE") {
			bVal = "1"
		}
		return fmt.Sprintf(`<%sc r="%s"%s t="b"><%sv>%s</%sv></%sc>`, nsPrefix, cellRef, styleAttr, nsPrefix, bVal, nsPrefix, nsPrefix)
	}
	if hasSST && internString != nil {
		sIdx := internString(rawVal)
		return fmt.Sprintf(`<%sc r="%s"%s t="s"><%sv>%d</%sv></%sc>`, nsPrefix, cellRef, styleAttr, nsPrefix, sIdx, nsPrefix, nsPrefix)
	}
	return fmt.Sprintf(`<%sc r="%s"%s t="inlineStr"><%sis><%st xml:space="preserve">%s</%st></%sis></%sc>`, nsPrefix, cellRef, styleAttr, nsPrefix, nsPrefix, xmlEscapeString(rawVal), nsPrefix, nsPrefix, nsPrefix)
}

func resolveXLSXRowElementIndex(rowOpenTag, rowInner string, seqRowIdx int) int {
	if rAttr := extractXMLAttr(rowOpenTag, "r"); rAttr != "" {
		if rNum, err := strconv.Atoi(rAttr); err == nil && rNum > 0 {
			return rNum - 1
		}
	}
	cStart, cOpenEnd, _, _, _, _, found := nextXMLElement(rowInner, 0, "c")
	if found {
		if rFromCell := excelRowIndex(extractXMLAttr(rowInner[cStart:cOpenEnd], "r")); rFromCell >= 0 {
			return rFromCell
		}
	}
	return seqRowIdx
}

func findXMLElementSpan(xmlStr, localName string) (start, openEnd, closeStart, closeEnd int, nsPrefix string, selfClosing, ok bool) {
	return nextXMLElement(xmlStr, 0, localName)
}

func isXMLTagNameByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '-' || b == '.' || b == ':'
}

func nextXMLElement(s string, from int, localName string) (start, openEnd, closeStart, closeEnd int, nsPrefix string, selfClosing, ok bool) {
	for pos := from; pos < len(s); {
		ltRel := strings.IndexByte(s[pos:], '<')
		if ltRel < 0 {
			return 0, 0, 0, 0, "", false, false
		}
		start = pos + ltRel
		if start+1 >= len(s) {
			return 0, 0, 0, 0, "", false, false
		}
		first := s[start+1]
		if first == '/' || first == '!' || first == '?' {
			pos = start + 2
			continue
		}
		afterName := start + 1
		for afterName < len(s) && isXMLTagNameByte(s[afterName]) {
			afterName++
		}
		if afterName == start+1 || afterName >= len(s) {
			pos = start + 1
			continue
		}
		nextCh := s[afterName]
		if nextCh != '>' && nextCh != '/' && nextCh != ' ' && nextCh != '\t' && nextCh != '\r' && nextCh != '\n' {
			pos = afterName
			continue
		}
		fullTag := s[start+1 : afterName]
		local := fullTag
		nsPrefix = ""
		if colon := strings.IndexByte(fullTag, ':'); colon >= 0 {
			nsPrefix = fullTag[:colon+1]
			local = fullTag[colon+1:]
		}
		if local != localName {
			pos = afterName
			continue
		}
		gtRel := strings.IndexByte(s[afterName:], '>')
		if gtRel < 0 {
			return 0, 0, 0, 0, "", false, false
		}
		openEnd = afterName + gtRel + 1
		trimmedTag := strings.TrimSpace(s[start : openEnd-1])
		if strings.HasSuffix(trimmedTag, "/") {
			return start, openEnd, openEnd, openEnd, nsPrefix, true, true
		}
		closeTag := "</" + fullTag + ">"
		closeRel := strings.Index(s[openEnd:], closeTag)
		if closeRel < 0 {
			return 0, 0, 0, 0, "", false, false
		}
		closeStart = openEnd + closeRel
		closeEnd = closeStart + len(closeTag)
		return start, openEnd, closeStart, closeEnd, nsPrefix, false, true
	}
	return 0, 0, 0, 0, "", false, false
}

func extractXMLAttr(openTag, attrName string) string {
	for _, quote := range []byte{'"', '\''} {
		pat := attrName + "=" + string(quote)
		for pos := 0; pos < len(openTag); {
			idx := strings.Index(openTag[pos:], pat)
			if idx < 0 {
				break
			}
			absIdx := pos + idx
			if absIdx > 0 {
				prev := openTag[absIdx-1]
				if prev != ' ' && prev != '\t' && prev != '\r' && prev != '\n' {
					pos = absIdx + len(pat)
					continue
				}
			}
			valStart := absIdx + len(pat)
			valEnd := strings.IndexByte(openTag[valStart:], quote)
			if valEnd < 0 {
				break
			}
			return openTag[valStart : valStart+valEnd]
		}
	}
	return ""
}

func sortedEditRowKeys(m map[int]map[int]string) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}
