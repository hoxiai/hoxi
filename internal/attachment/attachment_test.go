package attachment

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStoreSaveGetExtractAndHTTP(t *testing.T) {
	store := NewStore(t.TempDir())
	mux := http.NewServeMux()
	store.RegisterRoutes(mux)

	const tinyPNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	body, _ := json.Marshal(SaveRequest{
		SessionID: "ses_hoxi_1",
		Directory: "/tmp/proj",
		Filename:  "shot.png",
		Mime:      "image/png",
		DataURL:   tinyPNG,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/hoxi/attachments", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload image status = %d: %s", rec.Code, rec.Body.String())
	}
	var imgMeta Metadata
	if err := json.Unmarshal(rec.Body.Bytes(), &imgMeta); err != nil {
		t.Fatal(err)
	}
	if imgMeta.Kind != KindImage || !strings.HasPrefix(imgMeta.ID, "att_") || imgMeta.URL == "" {
		t.Fatalf("unexpected image metadata: %+v", imgMeta)
	}

	// Fetch content via HTTP
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, imgMeta.URL, nil))
	if getRec.Code != http.StatusOK || getRec.Header().Get("Content-Type") != "image/png" || getRec.Body.Len() == 0 {
		t.Fatalf("GET attachment content failed: code=%d ct=%s len=%d", getRec.Code, getRec.Header().Get("Content-Type"), getRec.Body.Len())
	}

	// Save text attachment and extract text + materialize to sandbox
	txtMeta, err := store.Save(SaveRequest{
		SessionID: "ses_hoxi_1",
		Filename:  "notes.md",
		Mime:      "text/markdown",
		Data:      []byte("# Architecture Notes\nUnified attachment service."),
	})
	if err != nil {
		t.Fatalf("save text attachment failed: %v", err)
	}
	if txtMeta.Kind != KindText {
		t.Fatalf("expected KindText, got %q", txtMeta.Kind)
	}
	if got := store.ExtractText(txtMeta.ID, 1000); !strings.Contains(got, "Unified attachment service.") {
		t.Fatalf("expected extracted markdown text, got %q", got)
	}
	wtDir := t.TempDir()
	_, matPath, err := store.MaterializeToDir(txtMeta.ID, wtDir)
	if err != nil {
		t.Fatalf("MaterializeToDir error: %v", err)
	}
	if raw, err := os.ReadFile(matPath); err != nil || !strings.Contains(string(raw), "Architecture Notes") {
		t.Fatalf("unexpected materialized file content: %v / %q", err, string(raw))
	}

	// Unsupported binary type must return error
	if _, err := store.Save(SaveRequest{
		Filename: "archive.zip",
		Mime:     "application/zip",
		Data:     []byte("PK\x03\x04"),
	}); err == nil {
		t.Fatal("expected unsupported file type error for .zip")
	}

	// Save and extract .xlsx spreadsheet
	var xlsxBuf bytes.Buffer
	zw := zip.NewWriter(&xlsxBuf)
	writeEntry := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(content))
	}
	writeEntry("xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8"?><workbook><sheets><sheet name="财务明细" sheetId="1" r:id="rId1"/></sheets></workbook>`)
	writeEntry("xl/sharedStrings.xml", `<?xml version="1.0" encoding="UTF-8"?><sst><si><t>项目</t></si><si><t>金额</t></si><si><t>服务器续费</t></si></sst>`)
	writeEntry("xl/worksheets/sheet1.xml", `<?xml version="1.0" encoding="UTF-8"?><worksheet><sheetData><row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row><row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2"><v>1280.5</v></c></row></sheetData></worksheet>`)
	_ = zw.Close()

	xlsxMeta, err := store.Save(SaveRequest{
		SessionID: "ses_hoxi_1",
		Filename:  "budget.xlsx",
		Data:      xlsxBuf.Bytes(),
	})
	if err != nil {
		t.Fatalf("save xlsx failed: %v", err)
	}
	if xlsxMeta.Kind != KindSpreadsheet {
		t.Fatalf("expected KindSpreadsheet, got %q", xlsxMeta.Kind)
	}
	sheetText := store.ExtractText(xlsxMeta.ID, 4000)
	if !strings.Contains(sheetText, "### Sheet: 财务明细") || !strings.Contains(sheetText, "项目\t金额") || !strings.Contains(sheetText, "服务器续费\t1280.5") {
		t.Fatalf("unexpected xlsx extracted text:\n%s", sheetText)
	}

	// Save and extract .pptx presentation + speaker notes
	var pptxBuf bytes.Buffer
	pzw := zip.NewWriter(&pptxBuf)
	writePPTX := func(name, content string) {
		w, err := pzw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(content))
	}
	writePPTX("ppt/slides/slide1.xml", `<?xml version="1.0" encoding="UTF-8"?><p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r><a:t>Q4 Product Roadmap</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>`)
	writePPTX("ppt/notesSlides/notesSlide1.xml", `<?xml version="1.0" encoding="UTF-8"?><p:notes xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r><a:t>Emphasize unified attachment architecture</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:notes>`)
	_ = pzw.Close()

	pptxMeta, err := store.Save(SaveRequest{
		SessionID: "ses_hoxi_2",
		Filename:  "roadmap.pptx",
		Data:      pptxBuf.Bytes(),
	})
	if err != nil {
		t.Fatalf("save pptx failed: %v", err)
	}
	if pptxMeta.Kind != KindDocument {
		t.Fatalf("expected KindDocument for pptx, got %q", pptxMeta.Kind)
	}
	pptxText := store.ExtractText(pptxMeta.ID, 4000)
	if !strings.Contains(pptxText, "### Slide 1") || !strings.Contains(pptxText, "Q4 Product Roadmap") || !strings.Contains(pptxText, "[Notes] Emphasize unified attachment architecture") {
		t.Fatalf("unexpected pptx extracted text:\n%s", pptxText)
	}

	// DeleteBySession removes ses_hoxi_1 attachments while preserving ses_hoxi_2
	removed := store.DeleteBySession("ses_hoxi_1")
	if removed != 3 {
		t.Fatalf("expected 3 attachments removed for ses_hoxi_1, got %d", removed)
	}
	if _, _, err := store.Get(xlsxMeta.ID); err == nil {
		t.Fatal("expected ses_hoxi_1 attachment to be deleted")
	}
	if _, _, err := store.Get(pptxMeta.ID); err != nil {
		t.Fatalf("expected ses_hoxi_2 attachment to remain intact: %v", err)
	}
}

func TestTruncateUTF8KeepsCharactersWhole(t *testing.T) {
	got := truncateUTF8("你好世界", 7)
	if got != "你好" {
		t.Fatalf("truncateUTF8 = %q, want %q", got, "你好")
	}
	if !utf8.ValidString(got) {
		t.Fatal("truncated string is not valid UTF-8")
	}
}

func TestActiveAttachmentContentIsSandboxed(t *testing.T) {
	for mime, want := range map[string]bool{
		"text/html; charset=utf-8": true,
		"image/svg+xml":            true,
		"application/rss+xml":      true,
		"image/png":                false,
		"application/pdf":          false,
		"text/plain":               false,
	} {
		if got := isActiveContentMime(mime); got != want {
			t.Fatalf("isActiveContentMime(%q) = %v, want %v", mime, got, want)
		}
	}
}

func TestExtractPDFTextPureGo(t *testing.T) {
	pdfBytes := []byte("%PDF-1.4\n1 0 obj\n<< /Length 55 >>\nstream\nBT\n(Hello Windows PDF) Tj\n0 -14 Td\n(Second Line) Tj\nET\nendstream\nendobj\n%%EOF\n")
	path := t.TempDir() + "/sample.pdf"
	if err := os.WriteFile(path, pdfBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	got := extractPDFTextPureGo(path)
	if !strings.Contains(got, "Hello Windows PDF") || !strings.Contains(got, "Second Line") {
		t.Fatalf("extractPDFTextPureGo = %q, want both lines", got)
	}
}

