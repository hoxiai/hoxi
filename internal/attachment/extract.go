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

	"github.com/hoxiai/hoxi/internal/subproc"
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
		cmd = subproc.CommandContext(ctx, "sips", "-s", "format", "png", inPath, "--out", outPath)
	} else {
		script := `Add-Type -AssemblyName System.Drawing; $img = [System.Drawing.Image]::FromFile($env:HOXI_IMG_IN); try { $img.Save($env:HOXI_IMG_OUT, [System.Drawing.Imaging.ImageFormat]::Png) } finally { $img.Dispose() }`
		cmd = subproc.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)
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
		cmd := subproc.CommandContext(ctx, "osascript", "-l", "JavaScript", "-e", macPDFExtractJXA, pdfPath)
		if out, err := cmd.Output(); err == nil {
			if s := strings.TrimSpace(string(out)); s != "" {
				return s
			}
		}
	}
	if bin, err := exec.LookPath("pdftotext"); err == nil && bin != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := subproc.CommandContext(ctx, bin, "-layout", "-nopgbrk", pdfPath, "-")
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
		cmd := subproc.CommandContext(ctx, "textutil", "-convert", "txt", "-stdout", docPath)
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
		cmd := subproc.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-WindowStyle", "Hidden", "-Command", script)
		cmd.Env = append(os.Environ(), "HOXI_DOC_PATH="+docPath)
		if out, err := cmd.Output(); err == nil {
			return strings.TrimSpace(string(out))
		}
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

// ExtractFileText extracts plain text from a file path on disk (PDF, Excel, Word, PPT, CSV, text).
func ExtractFileText(filePath string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 64000
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	kind, _, err := ClassifyFile(filepath.Base(filePath), "")
	if err != nil {
		switch ext {
		case ".pdf":
			kind = KindPDF
		case ".xlsx", ".xls", ".ods", ".csv", ".tsv":
			kind = KindSpreadsheet
		case ".docx", ".doc", ".pptx", ".ppt", ".rtf":
			kind = KindDocument
		default:
			kind = KindText
		}
	}

	var text string
	switch kind {
	case KindText:
		if f, err := os.Open(filePath); err == nil {
			raw, readErr := io.ReadAll(io.LimitReader(f, 4*1024*1024))
			_ = f.Close()
			if readErr == nil {
				text = strings.TrimSpace(string(raw))
			}
		}
	case KindSpreadsheet:
		switch ext {
		case ".csv", ".tsv":
			if f, err := os.Open(filePath); err == nil {
				raw, readErr := io.ReadAll(io.LimitReader(f, 4*1024*1024))
				_ = f.Close()
				if readErr == nil {
					text = strings.TrimSpace(string(raw))
				}
			}
		case ".xlsx":
			text = extractXLSXText(filePath)
		case ".xls":
			text = extractXLSText(filePath)
		case ".ods":
			text = extractODSText(filePath)
		default:
			text = extractXLSXText(filePath)
		}
	case KindDocument:
		switch ext {
		case ".docx":
			text = extractDOCXText(filePath)
		case ".pptx":
			text = extractPPTXText(filePath)
		default:
			text = extractMacTextUtil(filePath)
		}
	case KindPDF:
		text = extractPDFText(filePath)
	}
	if len(text) > maxChars {
		text = truncateUTF8(text, maxChars) + "\n...[truncated]..."
	}
	return text
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
		case ".xls":
			text = extractXLSText(blobPath)
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
