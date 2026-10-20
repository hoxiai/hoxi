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
}
