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
ObjC.import("AppKit");
ObjC.import("Quartz");
ObjC.import("Vision");
const args = $.NSProcessInfo.processInfo.arguments;
let result = "";
if (args.count >= 6) {
  // argv: osascript -l JavaScript -e <script> <pdfPath>
  const path = ObjC.unwrap(args.objectAtIndex(5));
  const url = $.NSURL.fileURLWithPath(path);
  const doc = $.PDFDocument.alloc.initWithURL(url);
  if (doc) {
    const rawStr = doc.string ? ObjC.unwrap(doc.string) : "";
    if (rawStr && rawStr.replace(/\s+/g, "").length >= 16) {
      result = rawStr;
    } else {
      const pageCount = Math.min(doc.pageCount, 8);
      const pagesOut = [];
      for (let p = 0; p < pageCount; p++) {
        const page = doc.pageAtIndex(p);
        if (!page) continue;
        const pageStr = page.string ? ObjC.unwrap(page.string).trim() : "";
        if (pageStr.length >= 16) {
          pagesOut.push("### Page " + (p + 1) + "\n" + pageStr);
          continue;
        }
        const thumb = page.thumbnailOfSizeForBox($.NSMakeSize(1600, 1600), 0);
        if (!thumb) continue;
        const tiff = thumb.TIFFRepresentation;
        if (!tiff) continue;
        const req = $.VNRecognizeTextRequest.alloc.init;
        req.recognitionLanguages = $(["zh-Hans", "zh-Hant", "en-US"]);
        const handler = $.VNImageRequestHandler.alloc.initWithDataOptions(tiff, $({}));
        handler.performRequestsError($([req]), null);
        const obs = req.results;
        const lines = [];
        if (obs) {
          for (let i = 0; i < obs.count; i++) {
            const cand = obs.objectAtIndex(i).topCandidates(1);
            if (cand && cand.count > 0) {
              lines.push(ObjC.unwrap(cand.objectAtIndex(0).string));
            }
          }
        }
        if (lines.length > 0) {
          pagesOut.push("### Page " + (p + 1) + " (OCR)\n" + lines.join("\n"));
        }
      }
      result = pagesOut.join("\n\n") || rawStr || "";
    }
  }
}
result;
`

func convertImageBytesToPNG(raw []byte, ext string) ([]byte, error) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
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
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.CommandContext(ctx, "sips", "-s", "format", "png", inPath, "--out", outPath)
	} else {
		script := `Add-Type -AssemblyName System.Drawing; $img = [System.Drawing.Image]::FromFile($env:HOXI_IMG_IN); try { $img.Save($env:HOXI_IMG_OUT, [System.Drawing.Imaging.ImageFormat]::Png) } finally { $img.Dispose() }`
		cmd = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)
		cmd.Env = append(os.Environ(), "HOXI_IMG_IN="+inPath, "HOXI_IMG_OUT="+outPath)
	}
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return os.ReadFile(outPath)
}

func extractPDFText(pdfPath string) string {
	if runtime.GOOS == "darwin" {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "osascript", "-l", "JavaScript", "-e", macPDFExtractJXA, pdfPath)
		if out, err := cmd.Output(); err == nil {
			if s := strings.TrimSpace(string(out)); s != "" {
				return s
			}
		}
	}
	if bin, err := exec.LookPath("pdftotext"); err == nil && bin != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "-layout", "-nopgbrk", pdfPath, "-")
		if out, err := cmd.Output(); err == nil {
			if s := strings.TrimSpace(string(out)); s != "" {
				return s
			}
		}
	}
	return extractPDFTextPureGo(pdfPath)
}

func extractMacTextUtil(docPath string) string {
	if runtime.GOOS == "darwin" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "textutil", "-convert", "txt", "-stdout", docPath)
		out, err := cmd.Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	if runtime.GOOS == "windows" && strings.EqualFold(filepath.Ext(docPath), ".rtf") {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		script := `Add-Type -AssemblyName System.Windows.Forms; $rtb = New-Object System.Windows.Forms.RichTextBox; try { $rtb.LoadFile($env:HOXI_DOC_PATH, [System.Windows.Forms.RichTextBoxStreamType]::RichText); [Console]::OutputEncoding = [System.Text.Encoding]::UTF8; Write-Output $rtb.Text } finally { $rtb.Dispose() }`
		cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-WindowStyle", "Hidden", "-Command", script)
		cmd.Env = append(os.Environ(), "HOXI_DOC_PATH="+docPath)
		if out, err := cmd.Output(); err == nil {
			return strings.TrimSpace(string(out))
		}
	}
	return ""
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

func extractDrawingMLParagraphs(f *zip.File) string {
	if f == nil {
		return ""
	}
	rc, err := f.Open()
	if err != nil {
		return ""
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
			case "br":
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
	return strings.TrimSpace(out.String())
}

func extractPPTXText(pptxPath string) string {
	zr, err := zip.OpenReader(pptxPath)
	if err != nil {
		return ""
	}
	defer zr.Close()

	type slideFile struct {
		num   int
		slide *zip.File
		notes *zip.File
	}
	slidesByNum := make(map[int]*slideFile)
	for _, f := range zr.File {
		name := strings.TrimPrefix(f.Name, "/")
		if strings.HasPrefix(name, "ppt/slides/slide") && strings.HasSuffix(name, ".xml") {
			numStr := strings.TrimSuffix(strings.TrimPrefix(name, "ppt/slides/slide"), ".xml")
			if n, err := strconv.Atoi(numStr); err == nil && n > 0 {
				sf := slidesByNum[n]
				if sf == nil {
					sf = &slideFile{num: n}
					slidesByNum[n] = sf
				}
				sf.slide = f
			}
		} else if strings.HasPrefix(name, "ppt/notesSlides/notesSlide") && strings.HasSuffix(name, ".xml") {
			numStr := strings.TrimSuffix(strings.TrimPrefix(name, "ppt/notesSlides/notesSlide"), ".xml")
			if n, err := strconv.Atoi(numStr); err == nil && n > 0 {
				sf := slidesByNum[n]
				if sf == nil {
					sf = &slideFile{num: n}
					slidesByNum[n] = sf
				}
				sf.notes = f
			}
		}
	}

	var nums []int
	for n, sf := range slidesByNum {
		if sf.slide != nil {
			nums = append(nums, n)
		}
	}
	sort.Ints(nums)

	var out strings.Builder
	for _, n := range nums {
		sf := slidesByNum[n]
		body := extractDrawingMLParagraphs(sf.slide)
		notes := extractDrawingMLParagraphs(sf.notes)
		if body == "" && notes == "" {
			continue
		}
		if out.Len() > 0 {
			out.WriteString("\n\n")
		}
		fmt.Fprintf(&out, "### Slide %d\n", n)
		if body != "" {
			out.WriteString(body)
		}
		if notes != "" {
			if body != "" {
				out.WriteByte('\n')
			}
			out.WriteString("[Notes] " + notes)
		}
	}
	return strings.TrimSpace(out.String())
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
		case ".pptx":
			text = extractPPTXText(blobPath)
		default:
			text = extractMacTextUtil(blobPath)
		}
	case KindPDF:
		text = extractPDFText(blobPath)
	}
	if len(text) > maxChars {
		text = truncateUTF8(text, maxChars) + "\n...[truncated]..."
	}
	return text
}
