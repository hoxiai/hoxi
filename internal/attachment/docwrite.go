package attachment

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/subproc"
)

func writeBytesAtomically(targetPath string, data []byte) error {
	return hoxipaths.WriteFileAtomic(targetPath, data, 0o644)
}

func xmlEscapeString(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func sanitizeSheetTitle(name string, idx int) string {
	cleaned := strings.TrimSpace(name)
	for _, ch := range []string{"\\", "/", "?", "*", ":", "[", "]"} {
		cleaned = strings.ReplaceAll(cleaned, ch, "_")
	}
	if cleaned == "" {
		return fmt.Sprintf("Sheet%d", idx)
	}
	runes := []rune(cleaned)
	if len(runes) > 31 {
		cleaned = string(runes[:31])
	}
	return cleaned
}

func isStrictNumber(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}
	if len(trimmed) > 1 && trimmed[0] == '0' && trimmed[1] != '.' {
		// Preserve leading-zero identifiers (like zip codes / SKUs "00123") as text.
		return false
	}
	if len(trimmed) > 15 {
		// Preserve long numeric IDs (like order numbers / bank card numbers) as text.
		return false
	}
	_, err := strconv.ParseFloat(trimmed, 64)
	return err == nil
}

// WriteXLSXFile creates an .xlsx workbook from one or more sheets using pure Go.
func WriteXLSXFile(targetPath string, sheets []SpreadsheetSheet) error {
	if len(sheets) == 0 {
		sheets = []SpreadsheetSheet{{Name: "Sheet1", Rows: [][]string{}}}
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addZipFile := func(name, body string) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(body))
		return err
	}

	var sharedStrings []string
	sharedIndex := make(map[string]int)
	internString := func(s string) int {
		if idx, ok := sharedIndex[s]; ok {
			return idx
		}
		idx := len(sharedStrings)
		sharedStrings = append(sharedStrings, s)
		sharedIndex[s] = idx
		return idx
	}

	var ctOverrides strings.Builder
	var wbSheets strings.Builder
	var wbRels strings.Builder

	for i, sheet := range sheets {
		sheetNum := i + 1
		sheetName := sanitizeSheetTitle(sheet.Name, sheetNum)
		fmt.Fprintf(&ctOverrides, `<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, sheetNum)
		fmt.Fprintf(&wbSheets, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, xmlEscapeString(sheetName), sheetNum, sheetNum)
		fmt.Fprintf(&wbRels, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, sheetNum, sheetNum)

		var ws strings.Builder
		ws.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
		ws.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
		for rIdx, row := range sheet.Rows {
			rowNum := rIdx + 1
			fmt.Fprintf(&ws, `<row r="%d">`, rowNum)
			for cIdx, rawVal := range row {
				val := strings.TrimSpace(rawVal)
				if val == "" {
					continue
				}
				cellRef := fmt.Sprintf("%s%d", ExcelColName(cIdx), rowNum)
				styleAttr := ""
				if rIdx == 0 {
					styleAttr = ` s="1"`
				}
				if strings.HasPrefix(val, "=") && len(val) > 1 {
					fmt.Fprintf(&ws, `<c r="%s"%s><f>%s</f></c>`, cellRef, styleAttr, xmlEscapeString(val[1:]))
				} else if isStrictNumber(val) {
					fmt.Fprintf(&ws, `<c r="%s"%s><v>%s</v></c>`, cellRef, styleAttr, xmlEscapeString(val))
				} else if strings.EqualFold(val, "TRUE") || strings.EqualFold(val, "FALSE") {
					bVal := "0"
					if strings.EqualFold(val, "TRUE") {
						bVal = "1"
					}
					fmt.Fprintf(&ws, `<c r="%s"%s t="b"><v>%s</v></c>`, cellRef, styleAttr, bVal)
				} else {
					sIdx := internString(rawVal)
					fmt.Fprintf(&ws, `<c r="%s"%s t="s"><v>%d</v></c>`, cellRef, styleAttr, sIdx)
				}
			}
			ws.WriteString(`</row>`)
		}
		ws.WriteString(`</sheetData></worksheet>`)
		if err := addZipFile(fmt.Sprintf("xl/worksheets/sheet%d.xml", sheetNum), ws.String()); err != nil {
			return err
		}
	}

	stylesRelID := len(sheets) + 1
	sstRelID := len(sheets) + 2
	fmt.Fprintf(&wbRels, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`, stylesRelID)
	fmt.Fprintf(&wbRels, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/sharedStrings" Target="sharedStrings.xml"/>`, sstRelID)

	contentTypes := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
		`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>` +
		`<Override PartName="/xl/sharedStrings.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"/>` +
		ctOverrides.String() + `</Types>`
	if err := addZipFile("[Content_Types].xml", contentTypes); err != nil {
		return err
	}

	rootRels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
		`</Relationships>`
	if err := addZipFile("_rels/.rels", rootRels); err != nil {
		return err
	}

	workbookXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
		`<sheets>` + wbSheets.String() + `</sheets></workbook>`
	if err := addZipFile("xl/workbook.xml", workbookXML); err != nil {
		return err
	}

	workbookRelsXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		wbRels.String() + `</Relationships>`
	if err := addZipFile("xl/_rels/workbook.xml.rels", workbookRelsXML); err != nil {
		return err
	}

	stylesXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
		`<fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><name val="Calibri"/></font></fonts>` +
		`<fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills>` +
		`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>` +
		`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
		`<cellXfs count="2"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/><xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/></cellXfs>` +
		`</styleSheet>`
	if err := addZipFile("xl/styles.xml", stylesXML); err != nil {
		return err
	}

	var sstXML strings.Builder
	sstXML.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	fmt.Fprintf(&sstXML, `<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="%d" uniqueCount="%d">`, len(sharedStrings), len(sharedStrings))
	for _, s := range sharedStrings {
		fmt.Fprintf(&sstXML, `<si><t xml:space="preserve">%s</t></si>`, xmlEscapeString(s))
	}
	sstXML.WriteString(`</sst>`)
	if err := addZipFile("xl/sharedStrings.xml", sstXML.String()); err != nil {
		return err
	}

	if err := zw.Close(); err != nil {
		return err
	}
	return writeBytesAtomically(targetPath, buf.Bytes())
}

// WriteDelimitedFile writes rows to a .csv or .tsv file atomically.
func WriteDelimitedFile(targetPath string, rows [][]string, comma rune) error {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if comma != 0 {
		w.Comma = comma
	}
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			return err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	return writeBytesAtomically(targetPath, buf.Bytes())
}

// ApplySpreadsheetEdits reads an existing spreadsheet (or creates a new one),
// applies cell edits (e.g. "B2": "1580.5") and/or appends rows to sheetName,
// and writes the updated spreadsheet to outPath.
func ApplySpreadsheetEdits(
	inPath, outPath, sheetName string,
	cellEdits map[string]string,
	appendRows [][]string,
) ([]SpreadsheetSheet, error) {
	if outPath == "" {
		outPath = inPath
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
	if len(cellEdits) > 0 {
		keys := make([]string, 0, len(cellEdits))
		for k := range cellEdits {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, ref := range keys {
			rIdx, cIdx, ok := ParseCellRef(ref)
			if !ok {
				return nil, fmt.Errorf("invalid cell reference %q (expected A1-style like B2)", ref)
			}
			for len(rows) <= rIdx {
				rows = append(rows, nil)
			}
			for len(rows[rIdx]) <= cIdx {
				rows[rIdx] = append(rows[rIdx], "")
			}
			rows[rIdx][cIdx] = cellEdits[ref]
		}
	}
	if len(appendRows) > 0 {
		rows = append(rows, appendRows...)
	}
	sheets[targetIdx].Rows = rows
	sheets[targetIdx].TotalRows = len(rows)

	ext := strings.ToLower(filepath.Ext(outPath))
	switch ext {
	case ".csv":
		return sheets, WriteDelimitedFile(outPath, sheets[targetIdx].Rows, ',')
	case ".tsv":
		return sheets, WriteDelimitedFile(outPath, sheets[targetIdx].Rows, '\t')
	default:
		return sheets, WriteXLSXFile(outPath, sheets)
	}
}

// WriteDOCXFile creates a standards-compliant .docx Word document from Markdown or plain text.
func WriteDOCXFile(targetPath, title, markdown string) error {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addZipFile := func(name, body string) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(body))
		return err
	}

	var bodyXML strings.Builder
	writePara := func(text string, style string, bold bool) {
		bodyXML.WriteString(`<w:p>`)
		if style != "" {
			fmt.Fprintf(&bodyXML, `<w:pPr><w:pStyle w:val="%s"/></w:pPr>`, xmlEscapeString(style))
		}
		bodyXML.WriteString(`<w:r>`)
		if bold {
			bodyXML.WriteString(`<w:rPr><w:b/></w:rPr>`)
		}
		fmt.Fprintf(&bodyXML, `<w:t xml:space="preserve">%s</w:t></w:r></w:p>`, xmlEscapeString(text))
	}

	writeTable := func(tableRows [][]string) {
		if len(tableRows) == 0 {
			return
		}
		bodyXML.WriteString(`<w:tbl><w:tblPr><w:tblBorders>` +
			`<w:top w:val="single" w:sz="4" w:space="0" w:color="CCCCCC"/>` +
			`<w:left w:val="single" w:sz="4" w:space="0" w:color="CCCCCC"/>` +
			`<w:bottom w:val="single" w:sz="4" w:space="0" w:color="CCCCCC"/>` +
			`<w:right w:val="single" w:sz="4" w:space="0" w:color="CCCCCC"/>` +
			`<w:insideH w:val="single" w:sz="4" w:space="0" w:color="CCCCCC"/>` +
			`<w:insideV w:val="single" w:sz="4" w:space="0" w:color="CCCCCC"/>` +
			`</w:tblBorders></w:tblPr>`)
		for rIdx, row := range tableRows {
			bodyXML.WriteString(`<w:tr>`)
			for _, cell := range row {
				bodyXML.WriteString(`<w:tc><w:p><w:r>`)
				if rIdx == 0 {
					bodyXML.WriteString(`<w:rPr><w:b/></w:rPr>`)
				}
				fmt.Fprintf(&bodyXML, `<w:t xml:space="preserve">%s</w:t></w:r></w:p></w:tc>`, xmlEscapeString(strings.TrimSpace(cell)))
			}
			bodyXML.WriteString(`</w:tr>`)
		}
		bodyXML.WriteString(`</w:tbl>`)
	}

	if strings.TrimSpace(title) != "" {
		writePara(strings.TrimSpace(title), "Heading1", true)
	}

	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	var pendingTable [][]string
	flushTable := func() {
		if len(pendingTable) > 0 {
			writeTable(pendingTable)
			pendingTable = nil
		}
	}

	for _, rawLine := range lines {
		trimmed := strings.TrimSpace(rawLine)
		if strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") && len(trimmed) > 2 {
			inner := strings.Trim(trimmed, "|")
			parts := strings.Split(inner, "|")
			isSeparator := true
			for _, p := range parts {
				sp := strings.Trim(strings.TrimSpace(p), ":-")
				if sp != "" {
					isSeparator = false
					break
				}
			}
			if isSeparator {
				continue
			}
			row := make([]string, len(parts))
			for i, p := range parts {
				row[i] = strings.TrimSpace(p)
			}
			pendingTable = append(pendingTable, row)
			continue
		}
		flushTable()
		if trimmed == "" {
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "### "):
			writePara(strings.TrimSpace(trimmed[4:]), "Heading3", true)
		case strings.HasPrefix(trimmed, "## "):
			writePara(strings.TrimSpace(trimmed[3:]), "Heading2", true)
		case strings.HasPrefix(trimmed, "# "):
			writePara(strings.TrimSpace(trimmed[2:]), "Heading1", true)
		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
			writePara("• "+strings.TrimSpace(trimmed[2:]), "", false)
		default:
			clean := strings.ReplaceAll(trimmed, "**", "")
			writePara(clean, "", false)
		}
	}
	flushTable()

	contentTypes := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
		`<Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>` +
		`</Types>`
	if err := addZipFile("[Content_Types].xml", contentTypes); err != nil {
		return err
	}

	rootRels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
		`</Relationships>`
	if err := addZipFile("_rels/.rels", rootRels); err != nil {
		return err
	}

	docRels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
		`</Relationships>`
	if err := addZipFile("word/_rels/document.xml.rels", docRels); err != nil {
		return err
	}

	stylesXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:rPr><w:b/><w:sz w:val="36"/></w:rPr></w:style>` +
		`<w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:rPr><w:b/><w:sz w:val="28"/></w:rPr></w:style>` +
		`<w:style w:type="paragraph" w:styleId="Heading3"><w:name w:val="heading 3"/><w:rPr><w:b/><w:sz w:val="24"/></w:rPr></w:style>` +
		`</w:styles>`
	if err := addZipFile("word/styles.xml", stylesXML); err != nil {
		return err
	}

	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		bodyXML.String() +
		`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/></w:sectPr>` +
		`</w:body></w:document>`
	if err := addZipFile("word/document.xml", docXML); err != nil {
		return err
	}

	if err := zw.Close(); err != nil {
		return err
	}
	return writeBytesAtomically(targetPath, buf.Bytes())
}

const macWritePDFJXA = `
ObjC.import("Foundation");
ObjC.import("AppKit");
ObjC.import("CoreGraphics");
const args = $.NSProcessInfo.processInfo.arguments;
if (args.count >= 7) {
  const inTxtPath = ObjC.unwrap(args.objectAtIndex(5));
  const outPdfPath = ObjC.unwrap(args.objectAtIndex(6));
  const rawStr = $.NSString.stringWithContentsOfFileEncodingError(inTxtPath, $.NSUTF8StringEncoding, null);
  const text = rawStr ? ObjC.unwrap(rawStr) : "";
  const tv = $.NSTextView.alloc.initWithFrame($.NSMakeRect(0, 0, 515, 760));
  tv.setString($(text));
  tv.setFont($.NSFont.systemFontOfSize(11.5));
  const pdfData = tv.dataWithPDFInsideRect(tv.bounds);
  if (pdfData) {
    pdfData.writeToFileAtomically(outPdfPath, true);
  }
}
`

// WritePDFFile generates a PDF file from text/Markdown content.
func WritePDFFile(targetPath, title, content string) error {
	fullText := strings.TrimSpace(content)
	if strings.TrimSpace(title) != "" {
		fullText = strings.TrimSpace(title) + "\n\n" + fullText
	}
	if runtime.GOOS == "darwin" && hasNonASCII(fullText) {
		if data, err := renderMacPDFBytes(fullText); err == nil && len(data) > 64 {
			return writeBytesAtomically(targetPath, data)
		}
	}
	data := buildPureGoPDF(fullText)
	return writeBytesAtomically(targetPath, data)
}

func hasNonASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return true
		}
	}
	return false
}

func renderMacPDFBytes(text string) ([]byte, error) {
	inFile, err := os.CreateTemp("", "hoxi-pdf-txt-*.txt")
	if err != nil {
		return nil, err
	}
	inPath := inFile.Name()
	defer os.Remove(inPath)
	if _, err := inFile.WriteString(text); err != nil {
		_ = inFile.Close()
		return nil, err
	}
	_ = inFile.Close()

	outFile, err := os.CreateTemp("", "hoxi-pdf-out-*.pdf")
	if err != nil {
		return nil, err
	}
	outPath := outFile.Name()
	_ = outFile.Close()
	defer os.Remove(outPath)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := subproc.CommandContext(ctx, "osascript", "-l", "JavaScript", "-e", macWritePDFJXA, inPath, outPath)
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return os.ReadFile(outPath)
}

func escapePDFLiteral(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '(':
			b.WriteString(`\(`)
		case ')':
			b.WriteString(`\)`)
		default:
			if r >= 32 && r <= 126 {
				b.WriteByte(byte(r))
			} else {
				b.WriteByte('?')
			}
		}
	}
	return b.String()
}

func buildPureGoPDF(text string) []byte {
	rawLines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var wrapped []string
	for _, line := range rawLines {
		runes := []rune(line)
		if len(runes) == 0 {
			wrapped = append(wrapped, "")
			continue
		}
		for len(runes) > 85 {
			wrapped = append(wrapped, string(runes[:85]))
			runes = runes[85:]
		}
		wrapped = append(wrapped, string(runes))
	}
	if len(wrapped) == 0 {
		wrapped = []string{""}
	}

	const linesPerPage = 48
	var pages [][]string
	for i := 0; i < len(wrapped); i += linesPerPage {
		end := i + linesPerPage
		if end > len(wrapped) {
			end = len(wrapped)
		}
		pages = append(pages, wrapped[i:end])
	}

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	totalObjects := 3 + len(pages)*2
	offsets := make([]int, totalObjects+1)

	writeObj := func(id int, body string) {
		offsets[id] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", id, body)
	}

	writeObj(1, "<< /Type /Catalog /Pages 2 0 R >>")

	var kids strings.Builder
	for i := range pages {
		pageObjID := 4 + i*2
		fmt.Fprintf(&kids, "%d 0 R ", pageObjID)
	}
	writeObj(2, fmt.Sprintf("<< /Type /Pages /Kids [ %s] /Count %d >>", kids.String(), len(pages)))
	writeObj(3, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")

	for i, pageLines := range pages {
		pageObjID := 4 + i*2
		contentObjID := pageObjID + 1
		var stream strings.Builder
		stream.WriteString("BT\n/F1 11 Tf\n50 780 Td\n")
		for lIdx, line := range pageLines {
			if lIdx > 0 {
				stream.WriteString("0 -15 Td\n")
			}
			fmt.Fprintf(&stream, "(%s) Tj\n", escapePDFLiteral(line))
		}
		stream.WriteString("ET\n")
		sBytes := stream.String()
		writeObj(pageObjID, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", contentObjID))
		writeObj(contentObjID, fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(sBytes), sBytes))
	}

	xrefPos := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", totalObjects+1)
	for id := 1; id <= totalObjects; id++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[id])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", totalObjects+1, xrefPos)
	return buf.Bytes()
}
