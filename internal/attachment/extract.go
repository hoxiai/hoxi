package attachment

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const macPDFExtractJXA = `
ObjC.import("Foundation");
ObjC.import("Quartz");
const args = $.NSProcessInfo.processInfo.arguments;
if (args.count >= 5) {
  const path = ObjC.unwrap(args.objectAtIndex(4));
  const url = $.NSURL.fileURLWithPath(path);
  const doc = $.PDFDocument.alloc.initWithURL(url);
  if (doc) {
    const str = doc.string;
    if (str) {
      ObjC.unwrap(str);
    } else {
      "";
    }
  } else {
    "";
  }
} else {
  "";
}
`

func convertImageBytesToPNG(raw []byte, ext string) ([]byte, error) {
	if runtime.GOOS != "darwin" {
		return nil, os.ErrInvalid
	}
	if ext == "" {
		ext = ".img"
	}
	inFile, err := os.CreateTemp("", "hoxi-img-in-*"+ext)
	if err != nil {
		return nil, err
	}
	inPath := inFile.Name()
	defer os.Remove(inPath)
	if _, err := inFile.Write(raw); err != nil {
		_ = inFile.Close()
		return nil, err
	}
	_ = inFile.Close()

	outFile, err := os.CreateTemp("", "hoxi-img-out-*.png")
	if err != nil {
		return nil, err
	}
	outPath := outFile.Name()
	_ = outFile.Close()
	defer os.Remove(outPath)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sips", "-s", "format", "png", inPath, "--out", outPath)
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return os.ReadFile(outPath)
}

func extractPDFText(pdfPath string) string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "osascript", "-l", "JavaScript", "-e", macPDFExtractJXA, pdfPath)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func extractMacTextUtil(docPath string) string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "textutil", "-convert", "txt", "-stdout", docPath)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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

func parseXLSXWorksheet(f *zip.File, shared []string, maxRows int) ([]string, bool) {
	rc, err := f.Open()
	if err != nil {
		return nil, false
	}
	defer rc.Close()

	type xmlCell struct {
		Ref      string `xml:"r,attr"`
		Type     string `xml:"t,attr"`
		Value    string `xml:"v"`
		InlineIS struct {
			Text string   `xml:"t"`
			Runs []string `xml:"r>t"`
		} `xml:"is"`
	}
	type xmlRow struct {
		Cells []xmlCell `xml:"c"`
	}

	dec := xml.NewDecoder(io.LimitReader(rc, 32*1024*1024))
	var lines []string
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
		if len(lines) >= maxRows {
			truncated = true
			break
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
			val = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(val), "\r\n", " "), "\n", " ")
			if val != "" {
				hasNonEmpty = true
			}
			cols[colIdx] = val
		}
		if hasNonEmpty {
			for len(cols) > 0 && cols[len(cols)-1] == "" {
				cols = cols[:len(cols)-1]
			}
			lines = append(lines, strings.Join(cols, "\t"))
		}
	}
	return lines, truncated
}

func extractXLSXText(xlsxPath string) string {
	zr, err := zip.OpenReader(xlsxPath)
	if err != nil {
		return ""
	}
	defer zr.Close()

	var shared []string
	for _, f := range zr.File {
		if strings.TrimPrefix(f.Name, "/") == "xl/sharedStrings.xml" {
			shared = parseXLSXSharedStrings(f)
			break
		}
	}

	sheets := resolveXLSXSheets(zr)
	var out strings.Builder
	for _, sheet := range sheets {
		rows, truncated := parseXLSXWorksheet(sheet.file, shared, 400)
		if len(rows) == 0 {
			continue
		}
		if out.Len() > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString("### Sheet: ")
		out.WriteString(sheet.name)
		out.WriteByte('\n')
		out.WriteString(strings.Join(rows, "\n"))
		if truncated {
			out.WriteString("\n...[rows truncated]...")
		}
	}
	return strings.TrimSpace(out.String())
}

func extractODSText(odsPath string) string {
	zr, err := zip.OpenReader(odsPath)
	if err != nil {
		return ""
	}
	defer zr.Close()

	for _, f := range zr.File {
		if strings.TrimPrefix(f.Name, "/") != "content.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return ""
		}
		defer rc.Close()

		dec := xml.NewDecoder(io.LimitReader(rc, 16*1024*1024))
		var out strings.Builder
		var rowCells []string
		var cellText strings.Builder
		inCell, inP := false, false
		for {
			tok, err := dec.Token()
			if err != nil {
				break
			}
			switch se := tok.(type) {
			case xml.StartElement:
				switch se.Name.Local {
				case "table":
					for _, attr := range se.Attr {
						if attr.Name.Local == "name" && attr.Value != "" {
							if out.Len() > 0 {
								out.WriteString("\n\n")
							}
							out.WriteString("### Sheet: " + attr.Value + "\n")
						}
					}
				case "table-row":
					rowCells = rowCells[:0]
				case "table-cell":
					inCell = true
					cellText.Reset()
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
						rowCells = append(rowCells, strings.TrimSpace(cellText.String()))
						inCell = false
					}
				case "table-row":
					nonEmpty := false
					for _, c := range rowCells {
						if c != "" {
							nonEmpty = true
							break
						}
					}
					if nonEmpty {
						out.WriteString(strings.Join(rowCells, "\t") + "\n")
					}
				}
			}
		}
		return strings.TrimSpace(out.String())
	}
	return ""
}

func extractDOCXText(docxPath string) string {
	zr, err := zip.OpenReader(docxPath)
	if err == nil {
		defer zr.Close()
		for _, f := range zr.File {
			if strings.TrimPrefix(f.Name, "/") != "word/document.xml" {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				break
			}
			defer rc.Close()

			dec := xml.NewDecoder(io.LimitReader(rc, 16*1024*1024))
			var out strings.Builder
			var para strings.Builder
			inT := false
			for {
				tok, err := dec.Token()
				if err != nil {
					break
				}
				switch se := tok.(type) {
				case xml.StartElement:
					switch se.Name.Local {
					case "t":
						inT = true
					case "tab":
						para.WriteByte('\t')
					case "br", "cr":
						para.WriteByte('\n')
					}
				case xml.CharData:
					if inT {
						para.Write(se)
					}
				case xml.EndElement:
					switch se.Name.Local {
					case "t":
						inT = false
					case "tc":
						para.WriteByte('\t')
					case "p", "tr":
						line := strings.TrimSpace(para.String())
						if line != "" {
							if out.Len() > 0 {
								out.WriteByte('\n')
							}
							out.WriteString(line)
						}
						para.Reset()
					}
				}
			}
			if res := strings.TrimSpace(out.String()); res != "" {
				return res
			}
		}
	}
	return extractMacTextUtil(docxPath)
}

func (s *Store) ExtractText(id string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 64000
	}
	meta, blobPath, err := s.Get(id)
	if err != nil {
		return ""
	}
	ext := strings.ToLower(filepath.Ext(meta.Filename))
	var text string
	switch meta.Kind {
	case KindText:
		raw, err := os.ReadFile(blobPath)
		if err == nil {
			text = strings.TrimSpace(string(raw))
		}
	case KindSpreadsheet:
		switch ext {
		case ".csv", ".tsv":
			raw, err := os.ReadFile(blobPath)
			if err == nil {
				text = strings.TrimSpace(string(raw))
			}
		case ".xlsx":
			text = extractXLSXText(blobPath)
		case ".ods":
			text = extractODSText(blobPath)
		default:
			text = extractXLSXText(blobPath)
		}
	case KindDocument:
		switch ext {
		case ".docx":
			text = extractDOCXText(blobPath)
		default:
			text = extractMacTextUtil(blobPath)
		}
	case KindPDF:
		text = extractPDFText(blobPath)
	}
	if len(text) > maxChars {
		text = text[:maxChars] + "\n...[truncated]..."
	}
	return text
}
