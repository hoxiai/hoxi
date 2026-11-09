package attachment

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	htmlRowRe    = regexp.MustCompile(`(?is)<tr\b[^>]*>(.*?)</tr>`)
	htmlCellRe   = regexp.MustCompile(`(?is)<t[dh]\b[^>]*>(.*?)</t[dh]>`)
	htmlTagStrip = regexp.MustCompile(`(?is)<[^>]+>`)
)

// SpreadsheetSheet holds parsed tabular rows for a single worksheet.
type SpreadsheetSheet struct {
	Name       string     `json:"name"`
	Rows       [][]string `json:"rows"`
	RowNumbers []int      `json:"rowNumbers,omitempty"`
	TotalRows  int        `json:"totalRows,omitempty"`
	Truncated  bool       `json:"truncated,omitempty"`
}

type spreadsheetParseOptions struct {
	maxRows            int
	preferFormulas     bool
	preserveEmptyRows  bool
	preserveExactCells bool
}

func excelColIndex(cellRef string) int {
	col := 0
	hasAlpha := false
	for i := 0; i < len(cellRef); i++ {
		c := cellRef[i]
		if c >= 'A' && c <= 'Z' {
			col = col*26 + int(c-'A'+1)
			hasAlpha = true
		} else if c >= 'a' && c <= 'z' {
			col = col*26 + int(c-'a'+1)
			hasAlpha = true
		} else {
			break
		}
	}
	if !hasAlpha {
		return -1
	}
	return col - 1
}

func excelRowIndex(cellRef string) int {
	i := 0
	for i < len(cellRef) && ((cellRef[i] >= 'A' && cellRef[i] <= 'Z') || (cellRef[i] >= 'a' && cellRef[i] <= 'z') || cellRef[i] == '$') {
		i++
	}
	if i >= len(cellRef) {
		return -1
	}
	numStr := strings.ReplaceAll(cellRef[i:], "$", "")
	r, err := strconv.Atoi(numStr)
	if err != nil || r <= 0 || r > 1048576 {
		return -1
	}
	return r - 1
}

// ExcelColName converts a 0-based column index into an Excel column name (0 -> "A", 25 -> "Z", 26 -> "AA").
func ExcelColName(colIdx int) string {
	if colIdx < 0 {
		return "A"
	}
	var buf [8]byte
	pos := len(buf)
	n := colIdx + 1
	for n > 0 {
		n--
		pos--
		buf[pos] = byte('A' + (n % 26))
		n /= 26
	}
	return string(buf[pos:])
}

// ParseCellRef splits an A1-style cell reference (e.g. "B12") into 0-based (row, col).
func ParseCellRef(ref string) (rowIdx, colIdx int, ok bool) {
	s := strings.TrimSpace(ref)
	if s == "" {
		return 0, 0, false
	}
	col := excelColIndex(s)
	if col < 0 || col > 16384 {
		return 0, 0, false
	}
	i := 0
	for i < len(s) && ((s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z')) {
		i++
	}
	if i >= len(s) {
		return 0, 0, false
	}
	r, err := strconv.Atoi(s[i:])
	if err != nil || r <= 0 || r > 1048576 {
		return 0, 0, false
	}
	return r - 1, col, true
}

func parseXLSXSharedStrings(f *zip.File) []string {
	rc, err := f.Open()
	if err != nil {
		return nil
	}
	defer rc.Close()

	dec := xml.NewDecoder(io.LimitReader(rc, 64*1024*1024))
	var result []string
	var inSI, inT bool
	var cur strings.Builder

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch se := tok.(type) {
		case xml.StartElement:
			switch se.Name.Local {
			case "si":
				inSI = true
				cur.Reset()
			case "t":
				if inSI {
					inT = true
				}
			}
		case xml.CharData:
			if inSI && inT {
				cur.Write(se)
			}
		case xml.EndElement:
			switch se.Name.Local {
			case "t":
				inT = false
			case "si":
				if inSI {
					result = append(result, cur.String())
					inSI = false
				}
			}
		}
	}
	return result
}

type xlsxSheetEntry struct {
	name string
	file *zip.File
}

func resolveXLSXSheets(zr *zip.ReadCloser) []xlsxSheetEntry {
	byName := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		byName[strings.TrimPrefix(f.Name, "/")] = f
	}

	relTarget := make(map[string]string)
	if relFile, ok := byName["xl/_rels/workbook.xml.rels"]; ok {
		if rc, err := relFile.Open(); err == nil {
			var rels struct {
				Items []struct {
					ID     string `xml:"Id,attr"`
					Target string `xml:"Target,attr"`
				} `xml:"Relationship"`
			}
			_ = xml.NewDecoder(io.LimitReader(rc, 4*1024*1024)).Decode(&rels)
			_ = rc.Close()
			for _, item := range rels.Items {
				target := strings.TrimPrefix(item.Target, "/")
				if !strings.HasPrefix(target, "xl/") {
					target = "xl/" + strings.TrimPrefix(target, "./")
				}
				relTarget[item.ID] = target
			}
		}
	}

	var entries []xlsxSheetEntry
	if wbFile, ok := byName["xl/workbook.xml"]; ok {
		if rc, err := wbFile.Open(); err == nil {
			dec := xml.NewDecoder(io.LimitReader(rc, 4*1024*1024))
			idx := 1
			for {
				tok, err := dec.Token()
				if err != nil {
					break
				}
				se, ok := tok.(xml.StartElement)
				if !ok || se.Name.Local != "sheet" {
					continue
				}
				var name, rID string
				for _, attr := range se.Attr {
					switch attr.Name.Local {
					case "name":
						name = attr.Value
					case "id":
						rID = attr.Value
					}
				}
				target := relTarget[rID]
				if target == "" {
					target = fmt.Sprintf("xl/worksheets/sheet%d.xml", idx)
				}
				if zf, exists := byName[target]; exists {
					if name == "" {
						name = fmt.Sprintf("Sheet%d", idx)
					}
					entries = append(entries, xlsxSheetEntry{name: name, file: zf})
				}
				idx++
			}
			_ = rc.Close()
		}
	}

	if len(entries) > 0 {
		return entries
	}

	var fallback []string
	for name := range byName {
		if strings.HasPrefix(name, "xl/worksheets/sheet") && strings.HasSuffix(name, ".xml") {
			fallback = append(fallback, name)
		}
	}
	sort.Strings(fallback)
	for i, name := range fallback {
		entries = append(entries, xlsxSheetEntry{
			name: fmt.Sprintf("Sheet%d", i+1),
			file: byName[name],
		})
	}
	return entries
}

func parseXLSXWorksheetRowsWithOpts(f *zip.File, shared []string, opts spreadsheetParseOptions) ([][]string, []int, int, bool) {
	rc, err := f.Open()
	if err != nil {
		return nil, nil, 0, false
	}
	defer rc.Close()

	type xmlCell struct {
		Ref      string `xml:"r,attr"`
		Type     string `xml:"t,attr"`
		Value    string `xml:"v"`
		Formula  string `xml:"f"`
		InlineIS struct {
			Text string   `xml:"t"`
			Runs []string `xml:"r>t"`
		} `xml:"is"`
	}
	type xmlRow struct {
		RowNum int       `xml:"r,attr"`
		Cells  []xmlCell `xml:"c"`
	}

	dec := xml.NewDecoder(io.LimitReader(rc, 64*1024*1024))
	var rows [][]string
	var rowNums []int
	totalRows := 0
	truncated := false
	seqRowIdx := 0

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "row" {
			continue
		}
		var row xmlRow
		if err := dec.DecodeElement(&row, &se); err != nil {
			continue
		}
		rowIdx := row.RowNum - 1
		if rowIdx < 0 {
			for _, c := range row.Cells {
				if rFromCell := excelRowIndex(c.Ref); rFromCell >= 0 {
					rowIdx = rFromCell
					break
				}
			}
		}
		if rowIdx < 0 {
			rowIdx = seqRowIdx
		}
		seqRowIdx = rowIdx + 1

		if len(row.Cells) == 0 && !opts.preserveEmptyRows {
			continue
		}
		var cols []string
		hasNonEmpty := false
		for i, c := range row.Cells {
			colIdx := excelColIndex(c.Ref)
			if colIdx < 0 || colIdx > 16384 {
				colIdx = i
			}
			for len(cols) <= colIdx {
				cols = append(cols, "")
			}
			val := c.Value
			if !opts.preserveExactCells {
				val = strings.TrimSpace(val)
			}
			switch c.Type {
			case "s":
				if idx, err := strconv.Atoi(strings.TrimSpace(c.Value)); err == nil && idx >= 0 && idx < len(shared) {
					val = shared[idx]
				}
			case "inlineStr":
				if c.InlineIS.Text != "" {
					val = c.InlineIS.Text
				} else if len(c.InlineIS.Runs) > 0 {
					val = strings.Join(c.InlineIS.Runs, "")
				}
			case "b":
				trimmedVal := strings.TrimSpace(c.Value)
				if trimmedVal == "1" || strings.EqualFold(trimmedVal, "true") {
					val = "TRUE"
				} else if trimmedVal == "0" || strings.EqualFold(trimmedVal, "false") {
					val = "FALSE"
				}
			}
			formulaText := strings.TrimSpace(c.Formula)
			if formulaText != "" && (opts.preferFormulas || strings.TrimSpace(val) == "") {
				val = "=" + strings.TrimPrefix(formulaText, "=")
			}
			if !opts.preserveExactCells {
				val = sanitizeCellText(val)
			}
			if strings.TrimSpace(val) != "" {
				hasNonEmpty = true
			}
			cols[colIdx] = val
		}
		if opts.preserveEmptyRows {
			cols = trimTrailingEmptyCells(cols)
			for len(rows) < rowIdx {
				if opts.maxRows > 0 && len(rows) >= opts.maxRows {
					truncated = true
					break
				}
				rows = append(rows, nil)
				rowNums = append(rowNums, len(rows))
				totalRows++
			}
			totalRows++
			if opts.maxRows <= 0 || len(rows) < opts.maxRows {
				if rowIdx < len(rows) {
					rows[rowIdx] = cols
				} else {
					rows = append(rows, cols)
					rowNums = append(rowNums, rowIdx+1)
				}
			} else {
				truncated = true
			}
			continue
		}
		if hasNonEmpty {
			cols = trimTrailingEmptyCells(cols)
			totalRows++
			if opts.maxRows <= 0 || len(rows) < opts.maxRows {
				rows = append(rows, cols)
				rowNums = append(rowNums, rowIdx+1)
			} else {
				truncated = true
			}
		}
	}
	return rows, rowNums, totalRows, truncated
}

func parseXLSXWorksheetRows(f *zip.File, shared []string, maxRows int) ([][]string, int, bool) {
	rows, _, totalRows, truncated := parseXLSXWorksheetRowsWithOpts(f, shared, spreadsheetParseOptions{maxRows: maxRows})
	return rows, totalRows, truncated
}

func sanitizeCellText(val string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(val), "\r\n", " "), "\n", " ")
}

func trimTrailingEmptyCells(cols []string) []string {
	for len(cols) > 0 && cols[len(cols)-1] == "" {
		cols = cols[:len(cols)-1]
	}
	return cols
}

func hasNonSequentialRowNumbers(rowNums []int) bool {
	for i, rn := range rowNums {
		if rn != i+1 {
			return true
		}
	}
	return false
}

func parseXLSXFileWithOpts(xlsxPath string, opts spreadsheetParseOptions) ([]SpreadsheetSheet, error) {
	zr, err := zip.OpenReader(xlsxPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	var shared []string
	for _, f := range zr.File {
		if strings.TrimPrefix(f.Name, "/") == "xl/sharedStrings.xml" {
			shared = parseXLSXSharedStrings(f)
			break
		}
	}

	entries := resolveXLSXSheets(zr)
	sheets := make([]SpreadsheetSheet, 0, len(entries))
	for _, entry := range entries {
		rows, rowNums, totalRows, truncated := parseXLSXWorksheetRowsWithOpts(entry.file, shared, opts)
		if len(rows) == 0 && !opts.preserveEmptyRows {
			continue
		}
		sheet := SpreadsheetSheet{
			Name:      entry.name,
			Rows:      rows,
			TotalRows: totalRows,
			Truncated: truncated,
		}
		if !opts.preserveEmptyRows && hasNonSequentialRowNumbers(rowNums) {
			sheet.RowNumbers = rowNums
		}
		sheets = append(sheets, sheet)
	}
	return sheets, nil
}

func parseXLSXFile(xlsxPath string, maxRows int) ([]SpreadsheetSheet, error) {
	return parseXLSXFileWithOpts(xlsxPath, spreadsheetParseOptions{maxRows: maxRows})
}

func parseDelimitedBytesWithOpts(raw []byte, comma rune, sheetName string, opts spreadsheetParseOptions) []SpreadsheetSheet {
	raw = bytes.TrimPrefix(raw, []byte("\xEF\xBB\xBF"))
	if comma == 0 {
		firstLine := string(raw)
		if idx := strings.IndexByte(firstLine, '\n'); idx >= 0 {
			firstLine = firstLine[:idx]
		}
		if strings.Count(firstLine, "\t") > strings.Count(firstLine, ",") {
			comma = '\t'
		} else {
			comma = ','
		}
	}
	r := csv.NewReader(bytes.NewReader(raw))
	r.Comma = comma
	r.FieldsPerRecord = -1
	r.LazyQuotes = true

	var rows [][]string
	var rowNums []int
	totalRows := 0
	truncated := false
	rawRowNum := 0
	for {
		rec, err := r.Read()
		if err != nil {
			break
		}
		rawRowNum++
		cleaned := make([]string, len(rec))
		hasNonEmpty := false
		for i, c := range rec {
			v := c
			if !opts.preserveExactCells {
				v = sanitizeCellText(c)
			}
			if strings.TrimSpace(v) != "" {
				hasNonEmpty = true
			}
			cleaned[i] = v
		}
		if !hasNonEmpty && !opts.preserveEmptyRows {
			continue
		}
		if !opts.preserveExactCells {
			cleaned = trimTrailingEmptyCells(cleaned)
		}
		totalRows++
		if opts.maxRows <= 0 || len(rows) < opts.maxRows {
			rows = append(rows, cleaned)
			rowNums = append(rowNums, rawRowNum)
		} else {
			truncated = true
		}
	}
	if len(rows) == 0 && !opts.preserveEmptyRows {
		return nil
	}
	if sheetName == "" {
		sheetName = "Sheet1"
	}
	sheet := SpreadsheetSheet{
		Name:      sheetName,
		Rows:      rows,
		TotalRows: totalRows,
		Truncated: truncated,
	}
	if !opts.preserveEmptyRows && hasNonSequentialRowNumbers(rowNums) {
		sheet.RowNumbers = rowNums
	}
	return []SpreadsheetSheet{sheet}
}

func parseDelimitedBytes(raw []byte, comma rune, sheetName string, maxRows int) []SpreadsheetSheet {
	return parseDelimitedBytesWithOpts(raw, comma, sheetName, spreadsheetParseOptions{maxRows: maxRows})
}

func parseSpreadsheetFileForEdit(path string) ([]SpreadsheetSheet, error) {
	opts := spreadsheetParseOptions{
		maxRows:            0, // unlimited
		preferFormulas:     true,
		preserveEmptyRows:  true,
		preserveExactCells: true,
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".xlsx":
		sheets, err := parseXLSXFileWithOpts(path, opts)
		if err == nil && len(sheets) > 0 {
			return sheets, nil
		}
		return parseXLSFile(path, 0)
	case ".xls":
		return parseXLSFile(path, 0)
	case ".ods":
		return parseODSFileWithOpts(path, opts)
	case ".csv", ".tsv":
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		raw, err := io.ReadAll(io.LimitReader(f, 64*1024*1024))
		if err != nil {
			return nil, err
		}
		comma := ','
		if ext == ".tsv" {
			comma = '\t'
		}
		base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		return parseDelimitedBytesWithOpts(raw, comma, base, opts), nil
	default:
		return parseXLSXFileWithOpts(path, opts)
	}
}

// ParseSpreadsheetFile parses .xlsx, .xls, .ods, .csv, or .tsv into structured sheets.
func ParseSpreadsheetFile(path string, maxRowsPerSheet int) ([]SpreadsheetSheet, error) {
	if maxRowsPerSheet <= 0 {
		maxRowsPerSheet = 400
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".xlsx":
		sheets, err := parseXLSXFile(path, maxRowsPerSheet)
		if err == nil && len(sheets) > 0 {
			return sheets, nil
		}
		return parseXLSFile(path, maxRowsPerSheet)
	case ".xls":
		return parseXLSFile(path, maxRowsPerSheet)
	case ".ods":
		return parseODSFile(path, maxRowsPerSheet)
	case ".csv", ".tsv":
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		raw, err := io.ReadAll(io.LimitReader(f, 64*1024*1024))
		if err != nil {
			return nil, err
		}
		comma := ','
		if ext == ".tsv" {
			comma = '\t'
		}
		base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		return parseDelimitedBytes(raw, comma, base, maxRowsPerSheet), nil
	default:
		return parseXLSXFile(path, maxRowsPerSheet)
	}
}

// FormatSpreadsheetSheets formats structured sheets into tab-separated text with sheet headers.
func FormatSpreadsheetSheets(sheets []SpreadsheetSheet) string {
	var out strings.Builder
	for _, sheet := range sheets {
		if len(sheet.Rows) == 0 {
			continue
		}
		if out.Len() > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString("### Sheet: ")
		out.WriteString(sheet.Name)
		out.WriteByte('\n')
		for i, row := range sheet.Rows {
			if i > 0 {
				out.WriteByte('\n')
			}
			out.WriteString(strings.Join(row, "\t"))
		}
		if sheet.Truncated {
			out.WriteString("\n...[rows truncated]...")
		}
	}
	return strings.TrimSpace(out.String())
}

func extractXLSXText(xlsxPath string) string {
	sheets, err := ParseSpreadsheetFile(xlsxPath, 400)
	if err != nil {
		return ""
	}
	return FormatSpreadsheetSheets(sheets)
}

func extractXLSText(xlsPath string) string {
	sheets, err := parseXLSFile(xlsPath, 400)
	if err != nil {
		return ""
	}
	return FormatSpreadsheetSheets(sheets)
}

func extractODSText(odsPath string) string {
	sheets, err := parseODSFile(odsPath, 400)
	if err != nil {
		return ""
	}
	return FormatSpreadsheetSheets(sheets)
}
