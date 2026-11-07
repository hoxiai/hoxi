package attachment

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"html"
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
	Name      string     `json:"name"`
	Rows      [][]string `json:"rows"`
	TotalRows int        `json:"totalRows,omitempty"`
	Truncated bool       `json:"truncated,omitempty"`
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
	if col < 0 || col > 1024 {
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
	if err != nil || r <= 0 || r > 100000 {
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

	dec := xml.NewDecoder(io.LimitReader(rc, 32*1024*1024))
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

func parseXLSXWorksheetRows(f *zip.File, shared []string, maxRows int) ([][]string, int, bool) {
	rc, err := f.Open()
	if err != nil {
		return nil, 0, false
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
		Cells []xmlCell `xml:"c"`
	}

	dec := xml.NewDecoder(io.LimitReader(rc, 32*1024*1024))
	var rows [][]string
	totalRows := 0
	truncated := false

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
		if len(row.Cells) == 0 {
			continue
		}
		var cols []string
		hasNonEmpty := false
		for i, c := range row.Cells {
			colIdx := excelColIndex(c.Ref)
			if colIdx < 0 || colIdx > 256 {
				colIdx = i
			}
			for len(cols) <= colIdx {
				cols = append(cols, "")
			}
			val := strings.TrimSpace(c.Value)
			switch c.Type {
			case "s":
				if idx, err := strconv.Atoi(val); err == nil && idx >= 0 && idx < len(shared) {
					val = shared[idx]
				}
			case "inlineStr":
				if c.InlineIS.Text != "" {
					val = c.InlineIS.Text
				} else if len(c.InlineIS.Runs) > 0 {
					val = strings.Join(c.InlineIS.Runs, "")
				}
			case "b":
				if val == "1" || strings.EqualFold(val, "true") {
					val = "TRUE"
				} else if val == "0" || strings.EqualFold(val, "false") {
					val = "FALSE"
				}
			}
			if val == "" && strings.TrimSpace(c.Formula) != "" {
				val = "=" + strings.TrimPrefix(strings.TrimSpace(c.Formula), "=")
			}
			val = sanitizeCellText(val)
			if val != "" {
				hasNonEmpty = true
			}
			cols[colIdx] = val
		}
		if hasNonEmpty {
			cols = trimTrailingEmptyCells(cols)
			totalRows++
			if maxRows <= 0 || len(rows) < maxRows {
				rows = append(rows, cols)
			} else {
				truncated = true
			}
		}
	}
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

func parseXLSXFile(xlsxPath string, maxRows int) ([]SpreadsheetSheet, error) {
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
		rows, totalRows, truncated := parseXLSXWorksheetRows(entry.file, shared, maxRows)
		if len(rows) == 0 {
			continue
		}
		sheets = append(sheets, SpreadsheetSheet{
			Name:      entry.name,
			Rows:      rows,
			TotalRows: totalRows,
			Truncated: truncated,
		})
	}
	return sheets, nil
}

func parseODSFile(odsPath string, maxRows int) ([]SpreadsheetSheet, error) {
	zr, err := zip.OpenReader(odsPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	for _, f := range zr.File {
		if strings.TrimPrefix(f.Name, "/") != "content.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()

		dec := xml.NewDecoder(io.LimitReader(rc, 16*1024*1024))
		var sheets []SpreadsheetSheet
		var curSheet *SpreadsheetSheet
		var rowCells []string
		var cellText strings.Builder
		cellRepeat := 1
		inCell, inP := false, false

		flushSheet := func() {
			if curSheet != nil && len(curSheet.Rows) > 0 {
				sheets = append(sheets, *curSheet)
			}
			curSheet = nil
		}

		for {
			tok, err := dec.Token()
			if err != nil {
				break
			}
			switch se := tok.(type) {
			case xml.StartElement:
				switch se.Name.Local {
				case "table":
					flushSheet()
					name := fmt.Sprintf("Sheet%d", len(sheets)+1)
					for _, attr := range se.Attr {
						if attr.Name.Local == "name" && attr.Value != "" {
							name = attr.Value
						}
					}
					curSheet = &SpreadsheetSheet{Name: name}
				case "table-row":
					rowCells = nil
				case "table-cell":
					inCell = true
					cellText.Reset()
					cellRepeat = 1
					for _, attr := range se.Attr {
						if attr.Name.Local == "number-columns-repeated" {
							if n, err := strconv.Atoi(attr.Value); err == nil && n > 1 && n <= 64 {
								cellRepeat = n
							}
						}
					}
				case "p":
					if inCell {
						inP = true
					}
				}
			case xml.CharData:
				if inCell && inP {
					cellText.Write(se)
				}
			case xml.EndElement:
				switch se.Name.Local {
				case "p":
					inP = false
				case "table-cell":
					if inCell {
						val := sanitizeCellText(cellText.String())
						for r := 0; r < cellRepeat; r++ {
							rowCells = append(rowCells, val)
						}
						inCell = false
					}
				case "table-row":
					if curSheet == nil {
						curSheet = &SpreadsheetSheet{Name: "Sheet1"}
					}
					trimmed := trimTrailingEmptyCells(append([]string(nil), rowCells...))
					if len(trimmed) > 0 {
						curSheet.TotalRows++
						if maxRows <= 0 || len(curSheet.Rows) < maxRows {
							curSheet.Rows = append(curSheet.Rows, trimmed)
						} else {
							curSheet.Truncated = true
						}
					}
				case "table":
					flushSheet()
				}
			}
		}
		flushSheet()
		return sheets, nil
	}
	return nil, os.ErrNotExist
}

func parseDelimitedBytes(raw []byte, comma rune, sheetName string, maxRows int) []SpreadsheetSheet {
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
	totalRows := 0
	truncated := false
	for {
		rec, err := r.Read()
		if err != nil {
			break
		}
		cleaned := make([]string, len(rec))
		hasNonEmpty := false
		for i, c := range rec {
			v := sanitizeCellText(c)
			if v != "" {
				hasNonEmpty = true
			}
			cleaned[i] = v
		}
		if !hasNonEmpty {
			continue
		}
		cleaned = trimTrailingEmptyCells(cleaned)
		totalRows++
		if maxRows <= 0 || len(rows) < maxRows {
			rows = append(rows, cleaned)
		} else {
			truncated = true
		}
	}
	if len(rows) == 0 {
		return nil
	}
	if sheetName == "" {
		sheetName = "Sheet1"
	}
	return []SpreadsheetSheet{{
		Name:      sheetName,
		Rows:      rows,
		TotalRows: totalRows,
		Truncated: truncated,
	}}
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
		raw, err := io.ReadAll(io.LimitReader(f, 16*1024*1024))
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

func parseXMLSpreadsheet2003(data []byte, maxRows int) []SpreadsheetSheet {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var sheets []SpreadsheetSheet
	var curSheet *SpreadsheetSheet
	var rowCells []string
	var cellData strings.Builder
	inCell, inData := false, false
	nextColIdx := 0

	flushSheet := func() {
		if curSheet != nil && len(curSheet.Rows) > 0 {
			sheets = append(sheets, *curSheet)
		}
		curSheet = nil
	}

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch se := tok.(type) {
		case xml.StartElement:
			switch se.Name.Local {
			case "Worksheet":
				flushSheet()
				name := fmt.Sprintf("Sheet%d", len(sheets)+1)
				for _, attr := range se.Attr {
					if attr.Name.Local == "Name" && attr.Value != "" {
						name = attr.Value
					}
				}
				curSheet = &SpreadsheetSheet{Name: name}
			case "Row":
				rowCells = nil
				nextColIdx = 0
			case "Cell":
				inCell = true
				cellData.Reset()
				for _, attr := range se.Attr {
					if attr.Name.Local == "Index" {
						if idx, err := strconv.Atoi(attr.Value); err == nil && idx > 0 && idx <= 256 {
							nextColIdx = idx - 1
						}
					}
				}
			case "Data":
				if inCell {
					inData = true
				}
			}
		case xml.CharData:
			if inCell && inData {
				cellData.Write(se)
			}
		case xml.EndElement:
			switch se.Name.Local {
			case "Data":
				inData = false
			case "Cell":
				if inCell {
					for len(rowCells) <= nextColIdx {
						rowCells = append(rowCells, "")
					}
					rowCells[nextColIdx] = sanitizeCellText(cellData.String())
					nextColIdx++
					inCell = false
				}
			case "Row":
				if curSheet == nil {
					curSheet = &SpreadsheetSheet{Name: "Sheet1"}
				}
				trimmed := trimTrailingEmptyCells(rowCells)
				if len(trimmed) > 0 {
					curSheet.TotalRows++
					if maxRows <= 0 || len(curSheet.Rows) < maxRows {
						curSheet.Rows = append(curSheet.Rows, trimmed)
					} else {
						curSheet.Truncated = true
					}
				}
			case "Worksheet":
				flushSheet()
			}
		}
	}
	flushSheet()
	return sheets
}

func parseHTMLTableSpreadsheet(data []byte, defaultName string, maxRows int) []SpreadsheetSheet {
	text := string(data)
	rowMatches := htmlRowRe.FindAllStringSubmatch(text, -1)
	if len(rowMatches) == 0 {
		return nil
	}
	var rows [][]string
	totalRows := 0
	truncated := false
	for _, rm := range rowMatches {
		cellMatches := htmlCellRe.FindAllStringSubmatch(rm[1], -1)
		if len(cellMatches) == 0 {
			continue
		}
		var cols []string
		hasVal := false
		for _, cm := range cellMatches {
			stripped := htmlTagStrip.ReplaceAllString(cm[1], " ")
			val := sanitizeCellText(html.UnescapeString(stripped))
			if val != "" {
				hasVal = true
			}
			cols = append(cols, val)
		}
		if !hasVal {
			continue
		}
		cols = trimTrailingEmptyCells(cols)
		totalRows++
		if maxRows <= 0 || len(rows) < maxRows {
			rows = append(rows, cols)
		} else {
			truncated = true
		}
	}
	if len(rows) == 0 {
		return nil
	}
	if defaultName == "" {
		defaultName = "Sheet1"
	}
	return []SpreadsheetSheet{{
		Name:      defaultName,
		Rows:      rows,
		TotalRows: totalRows,
		Truncated: truncated,
	}}
}
