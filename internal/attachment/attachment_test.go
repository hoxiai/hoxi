package attachment

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
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

func buildTestBIFF8OLE2XLS(sheetName string, sstStrings []string, cells []struct {
	row, col uint16
	sstIdx   int
	numVal   *float64
}) []byte {
	var wb bytes.Buffer
	writeRec := func(id uint16, payload []byte) {
		var hdr [4]byte
		binary.LittleEndian.PutUint16(hdr[0:2], id)
		binary.LittleEndian.PutUint16(hdr[2:4], uint16(len(payload)))
		wb.Write(hdr[:])
		wb.Write(payload)
	}

	// Workbook Globals BOF (dt = 0x0005)
	bofGlobals := make([]byte, 16)
	binary.LittleEndian.PutUint16(bofGlobals[0:2], 0x0600)
	binary.LittleEndian.PutUint16(bofGlobals[2:4], 0x0005)
	writeRec(biffRecBOF, bofGlobals)

	// BOUNDSHEET with UTF-16LE sheet name
	sheetRunes := utf16.Encode([]rune(sheetName))
	var bs bytes.Buffer
	_ = binary.Write(&bs, binary.LittleEndian, uint32(0)) // bof pos placeholder
	bs.WriteByte(0)                                       // visible
	bs.WriteByte(0)                                       // worksheet
	bs.WriteByte(byte(len(sheetRunes)))
	bs.WriteByte(1) // UTF-16LE
	for _, u := range sheetRunes {
		_ = binary.Write(&bs, binary.LittleEndian, u)
	}
	writeRec(biffRecBoundSheet, bs.Bytes())

	// SST record
	var sstBuf bytes.Buffer
	_ = binary.Write(&sstBuf, binary.LittleEndian, uint32(len(sstStrings)))
	_ = binary.Write(&sstBuf, binary.LittleEndian, uint32(len(sstStrings)))
	for _, s := range sstStrings {
		u16 := utf16.Encode([]rune(s))
		_ = binary.Write(&sstBuf, binary.LittleEndian, uint16(len(u16)))
		sstBuf.WriteByte(1) // UTF-16LE
		for _, ch := range u16 {
			_ = binary.Write(&sstBuf, binary.LittleEndian, ch)
		}
	}
	writeRec(biffRecSST, sstBuf.Bytes())
	writeRec(biffRecEOF, nil)

	// Worksheet BOF (dt = 0x0010)
	bofSheet := make([]byte, 16)
	binary.LittleEndian.PutUint16(bofSheet[0:2], 0x0600)
	binary.LittleEndian.PutUint16(bofSheet[2:4], 0x0010)
	writeRec(biffRecBOF, bofSheet)

	for _, cell := range cells {
		if cell.numVal != nil {
			var b [14]byte
			binary.LittleEndian.PutUint16(b[0:2], cell.row)
			binary.LittleEndian.PutUint16(b[2:4], cell.col)
			binary.LittleEndian.PutUint16(b[4:6], 0)
			binary.LittleEndian.PutUint64(b[6:14], math.Float64bits(*cell.numVal))
			writeRec(biffRecNumber, b[:])
		} else {
			var b [10]byte
			binary.LittleEndian.PutUint16(b[0:2], cell.row)
			binary.LittleEndian.PutUint16(b[2:4], cell.col)
			binary.LittleEndian.PutUint16(b[4:6], 0)
			binary.LittleEndian.PutUint32(b[6:10], uint32(cell.sstIdx))
			writeRec(biffRecLabelSST, b[:])
		}
	}
	writeRec(biffRecEOF, nil)

	stream := wb.Bytes()
	// Ensure stream >= 4096 bytes so it uses standard FAT sectors in this minimal OLE2 container
	if len(stream) < 4096 {
		padded := make([]byte, 4096)
		copy(padded, stream)
		stream = padded
	}
	numStreamSectors := (len(stream) + 511) / 512

	// Sector layout:
	// Sector 0: FAT
	// Sector 1: Directory
	// Sector 2..(2+numStreamSectors-1): Workbook stream
	totalSectors := 2 + numStreamSectors
	fileBytes := make([]byte, (1+totalSectors)*512)

	// Header
	copy(fileBytes[0:8], ole2Signature)
	binary.LittleEndian.PutUint16(fileBytes[24:26], 0x003E)
	binary.LittleEndian.PutUint16(fileBytes[26:28], 0x0003)
	binary.LittleEndian.PutUint16(fileBytes[28:30], 0xFFFE)
	binary.LittleEndian.PutUint16(fileBytes[30:32], 9) // 512-byte sectors
	binary.LittleEndian.PutUint16(fileBytes[32:34], 6) // 64-byte mini sectors
	binary.LittleEndian.PutUint32(fileBytes[44:48], 1) // 1 FAT sector
	binary.LittleEndian.PutUint32(fileBytes[48:52], 1) // Dir starts at sector 1
	binary.LittleEndian.PutUint32(fileBytes[56:60], 4096)
	binary.LittleEndian.PutUint32(fileBytes[60:64], oleEndOfChain)
	binary.LittleEndian.PutUint32(fileBytes[68:72], oleEndOfChain)
	binary.LittleEndian.PutUint32(fileBytes[76:80], 0) // DIFAT[0] = sector 0
	for i := 1; i < 109; i++ {
		binary.LittleEndian.PutUint32(fileBytes[76+i*4:80+i*4], oleFreeSect)
	}

	// Sector 0: FAT (at file offset 512)
	fat := fileBytes[512 : 1024]
	for i := 0; i < 128; i++ {
		binary.LittleEndian.PutUint32(fat[i*4:(i+1)*4], oleFreeSect)
	}
	binary.LittleEndian.PutUint32(fat[0:4], 0xFFFFFFFD)      // Sector 0: FATSECT
	binary.LittleEndian.PutUint32(fat[4:8], oleEndOfChain)   // Sector 1: Directory
	for s := 0; s < numStreamSectors; s++ {
		secID := 2 + s
		next := uint32(secID + 1)
		if s == numStreamSectors-1 {
			next = oleEndOfChain
		}
		binary.LittleEndian.PutUint32(fat[secID*4:(secID+1)*4], next)
	}

	// Sector 1: Directory (at file offset 1024)
	dirSec := fileBytes[1024 : 1536]
	writeDirEntry := func(off int, name string, objType byte, startSec uint32, size uint32) {
		ent := dirSec[off : off+128]
		u16 := utf16.Encode([]rune(name))
		for i, ch := range u16 {
			binary.LittleEndian.PutUint16(ent[i*2:i*2+2], ch)
		}
		binary.LittleEndian.PutUint16(ent[64:66], uint16((len(u16)+1)*2))
		ent[66] = objType
		binary.LittleEndian.PutUint32(ent[116:120], startSec)
		binary.LittleEndian.PutUint32(ent[120:124], size)
	}
	writeDirEntry(0, "Root Entry", 5, oleEndOfChain, 0)
	writeDirEntry(128, "Workbook", 2, 2, uint32(len(stream)))

	// Sectors 2..: Stream data
	copy(fileBytes[1536:], stream)
	return fileBytes
}

func TestExtractXLSFormatsAndDocReadWrite(t *testing.T) {
	store := NewStore(t.TempDir())

	// 1. Legacy OLE2 + BIFF8 .xls extraction
	val := 9527.5
	xlsBytes := buildTestBIFF8OLE2XLS("Q4营收", []string{"渠道", "销售额", "直营店"}, []struct {
		row, col uint16
		sstIdx   int
		numVal   *float64
	}{
		{row: 0, col: 0, sstIdx: 0},
		{row: 0, col: 1, sstIdx: 1},
		{row: 1, col: 0, sstIdx: 2},
		{row: 1, col: 1, numVal: &val},
	})
	xlsMeta, err := store.Save(SaveRequest{
		SessionID: "ses_xls",
		Filename:  "q4_report.xls",
		Data:      xlsBytes,
	})
	if err != nil {
		t.Fatalf("save .xls failed: %v", err)
	}
	xlsText := store.ExtractText(xlsMeta.ID, 4000)
	if !strings.Contains(xlsText, "### Sheet: Q4营收") || !strings.Contains(xlsText, "渠道\t销售额") || !strings.Contains(xlsText, "直营店\t9527.5") {
		t.Fatalf("unexpected BIFF8 .xls extracted text:\n%s", xlsText)
	}

	// 2. HTML Table disguised as .xls (common ERP export)
	htmlXLS := []byte(`<html><body><table><tr><th>订单号</th><th>状态</th></tr><tr><td>ORD-1001</td><td>已发货</td></tr></table></body></html>`)
	htmlMeta, err := store.Save(SaveRequest{
		SessionID: "ses_xls",
		Filename:  "erp_export.xls",
		Data:      htmlXLS,
	})
	if err != nil {
		t.Fatalf("save html .xls failed: %v", err)
	}
	htmlText := store.ExtractText(htmlMeta.ID, 4000)
	if !strings.Contains(htmlText, "订单号\t状态") || !strings.Contains(htmlText, "ORD-1001\t已发货") {
		t.Fatalf("unexpected HTML .xls extracted text:\n%s", htmlText)
	}

	// 3. WriteXLSXFile + ApplySpreadsheetEdits roundtrip
	tmpDir := t.TempDir()
	xlsxPath := filepath.Join(tmpDir, "finance.xlsx")
	if err := WriteXLSXFile(xlsxPath, []SpreadsheetSheet{
		{
			Name: "汇总",
			Rows: [][]string{
				{"月份", "收入", "支出"},
				{"10月", "50000", "18000"},
			},
		},
	}); err != nil {
		t.Fatalf("WriteXLSXFile failed: %v", err)
	}
	_, err = ApplySpreadsheetEdits(
		xlsxPath,
		xlsxPath,
		"汇总",
		map[string]string{"B2": "56000", "D1": "净利润", "D2": "=B2-C2"},
		[][]string{{"11月", "62000", "20000", "=B3-C3"}},
	)
	if err != nil {
		t.Fatalf("ApplySpreadsheetEdits failed: %v", err)
	}
	parsedSheets, err := ParseSpreadsheetFile(xlsxPath, 100)
	if err != nil || len(parsedSheets) != 1 {
		t.Fatalf("ParseSpreadsheetFile after edit failed: %v, sheets=%d", err, len(parsedSheets))
	}
	if len(parsedSheets[0].Rows) != 3 || parsedSheets[0].Rows[1][1] != "56000" || parsedSheets[0].Rows[2][0] != "11月" {
		t.Fatalf("unexpected rows after edit: %+v", parsedSheets[0].Rows)
	}

	// 4. WriteDOCXFile + ExtractFileText roundtrip
	docxPath := filepath.Join(tmpDir, "summary.docx")
	mdContent := "# 季度复盘\n\n- 核心指标达成率 115%\n\n| 指标 | 数值 |\n| --- | --- |\n| GMV | 128万 |\n"
	if err := WriteDOCXFile(docxPath, "2026 Q4 经营报告", mdContent); err != nil {
		t.Fatalf("WriteDOCXFile failed: %v", err)
	}
	docxExtracted := ExtractFileText(docxPath, 4000)
	if !strings.Contains(docxExtracted, "2026 Q4 经营报告") || !strings.Contains(docxExtracted, "GMV") || !strings.Contains(docxExtracted, "128万") {
		t.Fatalf("unexpected docx extracted text:\n%s", docxExtracted)
	}

	// 5. WritePDFFile + ExtractFileText roundtrip
	pdfPath := filepath.Join(tmpDir, "brief.pdf")
	if err := WritePDFFile(pdfPath, "Executive Brief", "Quarterly target achieved Ahead of Schedule."); err != nil {
		t.Fatalf("WritePDFFile failed: %v", err)
	}
	pdfExtracted := ExtractFileText(pdfPath, 4000)
	if !strings.Contains(pdfExtracted, "Executive Brief") || !strings.Contains(pdfExtracted, "Quarterly target achieved") {
		t.Fatalf("unexpected pdf extracted text:\n%s", pdfExtracted)
	}

	// 6. WritePPTXFile + ExtractFileText roundtrip
	pptxPath := filepath.Join(tmpDir, "roadmap.pptx")
	slidesMD := "# 2027 战略目标\n- 营收增长 150%\n- 自动化覆盖率 90%\n---\n## Q1 关键里程碑\n- 发布多运行时工作台\n"
	if err := WritePPTXFile(pptxPath, "年度战略规划", slidesMD); err != nil {
		t.Fatalf("WritePPTXFile failed: %v", err)
	}
	pptxExtracted := ExtractFileText(pptxPath, 4000)
	if !strings.Contains(pptxExtracted, "2027 战略目标") || !strings.Contains(pptxExtracted, "Q1 关键里程碑") || !strings.Contains(pptxExtracted, "发布多运行时工作台") {
		t.Fatalf("unexpected pptx extracted text:\n%s", pptxExtracted)
	}
}

func TestApplySpreadsheetEditsPreservesCachedFormulasAndOver50kRows(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	// 1. XLSX with cached formula value (<f>A1*2</f><v>20</v>) must keep its formula when A1 is edited.
	xlsxPath := filepath.Join(tmpDir, "formulas.xlsx")
	var xlsxBuf bytes.Buffer
	zw := zip.NewWriter(&xlsxBuf)
	writeZipEntry := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	writeZipEntry("xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8"?><workbook><sheets><sheet name="Sheet1" sheetId="1" r:id="rId1"/></sheets></workbook>`)
	writeZipEntry("xl/worksheets/sheet1.xml", `<?xml version="1.0" encoding="UTF-8"?><worksheet><sheetData><row r="1"><c r="A1"><v>10</v></c><c r="B1"><f>A1*2</f><v>20</v></c></row></sheetData></worksheet>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(xlsxPath, xlsxBuf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	sheets, err := ApplySpreadsheetEdits(xlsxPath, xlsxPath, "Sheet1", map[string]string{"A1": "15"}, nil)
	if err != nil {
		t.Fatalf("ApplySpreadsheetEdits on formula workbook failed: %v", err)
	}
	if len(sheets) != 1 || len(sheets[0].Rows) != 1 || len(sheets[0].Rows[0]) < 2 {
		t.Fatalf("unexpected sheets after edit: %+v", sheets)
	}
	if got := sheets[0].Rows[0][1]; got != "=A1*2" {
		t.Fatalf("expected B1 formula '=A1*2' to be preserved in memory, got %q", got)
	}
	// Verify written XML still contains <f>A1*2</f>
	zr, err := zip.OpenReader(xlsxPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	foundFormula := false
	for _, f := range zr.File {
		if f.Name == "xl/worksheets/sheet1.xml" {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			var raw bytes.Buffer
			_, _ = raw.ReadFrom(rc)
			_ = rc.Close()
			if strings.Contains(raw.String(), "<f>A1*2</f>") {
				foundFormula = true
			}
		}
	}
	if !foundFormula {
		t.Fatalf("expected written worksheet XML to preserve <f>A1*2</f>")
	}

	// 2. 50,001-row CSV must not lose row 50,001 when A1 is edited.
	csvPath := filepath.Join(tmpDir, "large.csv")
	var csvBuf bytes.Buffer
	for i := 1; i <= 50001; i++ {
		fmt.Fprintf(&csvBuf, "row_%d,val_%d\n", i, i)
	}
	if err := os.WriteFile(csvPath, csvBuf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	csvSheets, err := ApplySpreadsheetEdits(csvPath, csvPath, "", map[string]string{"A1": "edited_header"}, nil)
	if err != nil {
		t.Fatalf("ApplySpreadsheetEdits on 50,001-row CSV failed: %v", err)
	}
	if len(csvSheets) != 1 || len(csvSheets[0].Rows) != 50001 {
		t.Fatalf("expected all 50,001 rows preserved, got %d", len(csvSheets[0].Rows))
	}
	if got := csvSheets[0].Rows[0][0]; got != "edited_header" {
		t.Fatalf("expected A1 = 'edited_header', got %q", got)
	}
	if got := csvSheets[0].Rows[50000][0]; got != "row_50001" {
		t.Fatalf("expected last row 'row_50001' preserved, got %q", got)
	}

	// 3. Sparse XLSX with empty row 2 (rows 1 and 3 populated) returns RowNumbers [1, 3] in preview
	// and editing A3 updates row 3 without shifting it into row 2.
	sparsePath := filepath.Join(tmpDir, "sparse.xlsx")
	if err := WriteXLSXFile(sparsePath, []SpreadsheetSheet{{
		Name: "Sheet1",
		Rows: [][]string{
			{"Header"},
			{},
			{"Row3Val"},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	preview, err := ParseSpreadsheetFile(sparsePath, 100)
	if err != nil || len(preview) != 1 {
		t.Fatalf("ParseSpreadsheetFile on sparse.xlsx failed: %v", err)
	}
	if len(preview[0].Rows) != 2 || len(preview[0].RowNumbers) != 2 || preview[0].RowNumbers[0] != 1 || preview[0].RowNumbers[1] != 3 {
		t.Fatalf("expected preview RowNumbers [1, 3], got rows=%+v rowNumbers=%+v", preview[0].Rows, preview[0].RowNumbers)
	}
	if _, err := ApplySpreadsheetEdits(sparsePath, sparsePath, "Sheet1", map[string]string{"A3": "UpdatedRow3"}, nil); err != nil {
		t.Fatal(err)
	}
	afterSparse, err := ParseSpreadsheetFile(sparsePath, 100)
	if err != nil || len(afterSparse) != 1 {
		t.Fatal(err)
	}
	if len(afterSparse[0].Rows) != 2 || afterSparse[0].Rows[1][0] != "UpdatedRow3" || afterSparse[0].RowNumbers[1] != 3 {
		t.Fatalf("expected row 3 updated in place, got rows=%+v rowNumbers=%+v", afterSparse[0].Rows, afterSparse[0].RowNumbers)
	}
}

func TestDeepXLSXEditPreservesUntouchedMergedCells(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "merged.xlsx")
	if err := WriteXLSXFile(file, []SpreadsheetSheet{
		{Name: "Inputs", Rows: [][]string{{"1"}}},
		{Name: "Report", Rows: [][]string{{"Title", ""}, {"Description", "text"}}},
	}); err != nil {
		t.Fatal(err)
	}
	var fixture bytes.Buffer
	zw := zip.NewWriter(&fixture)
	original, err := zip.OpenReader(file)
	if err != nil {
		t.Fatal(err)
	}
	marker := []byte(`<mergeCells count="1"><mergeCell ref="A1:B1"/></mergeCells>`)
	for _, z := range original.File {
		r, err := z.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if z.Name == "xl/worksheets/sheet2.xml" {
			data = bytes.Replace(data, []byte("</sheetData>"), append([]byte("</sheetData>"), marker...), 1)
		}
		out, err := zw.Create(z.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := original.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, fixture.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplySpreadsheetEdits(file, file, "Inputs", map[string]string{"A1": "2"}, nil); err != nil {
		t.Fatal(err)
	}
	edited, err := zip.OpenReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer edited.Close()
	for _, z := range edited.File {
		if z.Name == "xl/worksheets/sheet2.xml" {
			r, _ := z.Open()
			data, _ := io.ReadAll(r)
			_ = r.Close()
			if !bytes.Contains(data, marker) {
				t.Fatalf("editing Inputs!A1 removed Report!A1:B1 merged range: %s", data)
			}
			return
		}
	}
	t.Fatal("Report sheet missing")
}

func TestDeepOversizeCSVEditDoesNotTruncate(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "oversize.csv")
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	chunk := strings.Repeat(strings.Repeat("z", 1022)+",\n", 1024)
	for i := 0; i < 64; i++ {
		_, _ = f.WriteString(chunk)
	}
	marker := "BEYOND_READ_LIMIT,42\n"
	_, _ = f.WriteString(marker)
	_ = f.Close()
	if _, err := ApplySpreadsheetEdits(file, file, "oversize", map[string]string{"A1": "edited"}, nil); err != nil {
		t.Fatalf("ApplySpreadsheetEdits on oversize CSV returned error: %v", err)
	}
	f, _ = os.Open(file)
	defer f.Close()
	_, _ = f.Seek(-int64(len(marker)), io.SeekEnd)
	tail, _ := io.ReadAll(f)
	if string(tail) != marker {
		t.Fatalf("CSV tail beyond 64 MiB lost after edit; tail=%q", tail)
	}
}

func TestVerifyPrefixedXLSXPreservesUntouchedMergedCells(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "prefixed.xlsx")
	if err := WriteXLSXFile(file, []SpreadsheetSheet{
		{Name: "Inputs", Rows: [][]string{{"1"}}},
		{Name: "Report", Rows: [][]string{{"Title", ""}, {"A", "B"}}},
	}); err != nil {
		t.Fatal(err)
	}
	original, err := zip.OpenReader(file)
	if err != nil {
		t.Fatal(err)
	}
	var fixture bytes.Buffer
	zw := zip.NewWriter(&fixture)
	marker := []byte(`<x:mergeCells count="1"><x:mergeCell ref="A1:B1"/></x:mergeCells>`)
	for _, z := range original.File {
		r, _ := z.Open()
		data, _ := io.ReadAll(r)
		_ = r.Close()
		if z.Name == "xl/worksheets/sheet1.xml" || z.Name == "xl/worksheets/sheet2.xml" {
			data = bytes.ReplaceAll(data, []byte(`<worksheet xmlns="`), []byte(`<x:worksheet xmlns:x="`))
			data = bytes.ReplaceAll(data, []byte(`</worksheet>`), []byte(`</x:worksheet>`))
			for _, tag := range []string{"sheetData", "row", "c", "v", "f", "is", "t"} {
				data = bytes.ReplaceAll(data, []byte("<"+tag+">"), []byte("<x:"+tag+">"))
				data = bytes.ReplaceAll(data, []byte("<"+tag+" "), []byte("<x:"+tag+" "))
				data = bytes.ReplaceAll(data, []byte("</"+tag+">"), []byte("</x:"+tag+">"))
			}
		}
		if z.Name == "xl/worksheets/sheet2.xml" {
			data = bytes.Replace(data, []byte("</x:sheetData>"), append([]byte("</x:sheetData>"), marker...), 1)
		}
		if z.Name == "xl/sharedStrings.xml" {
			data = bytes.ReplaceAll(data, []byte(`<sst xmlns="`), []byte(`<x:sst xmlns:x="`))
			data = bytes.ReplaceAll(data, []byte(`</sst>`), []byte(`</x:sst>`))
			for _, tag := range []string{"si", "t"} {
				data = bytes.ReplaceAll(data, []byte("<"+tag+">"), []byte("<x:"+tag+">"))
				data = bytes.ReplaceAll(data, []byte("<"+tag+" "), []byte("<x:"+tag+" "))
				data = bytes.ReplaceAll(data, []byte("</"+tag+">"), []byte("</x:"+tag+">"))
			}
		}
		out, _ := zw.Create(z.Name)
		_, _ = out.Write(data)
	}
	_ = original.Close()
	_ = zw.Close()
	if err := os.WriteFile(file, fixture.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	sheets, err := ApplySpreadsheetEdits(file, file, "Inputs", map[string]string{"A1": "new-val"}, nil)
	if err != nil || len(sheets) < 1 || sheets[0].Rows[0][0] != "new-val" {
		t.Fatalf("unexpected Inputs!A1 after edit: err=%v sheets=%+v", err, sheets)
	}
	edited, err := zip.OpenReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer edited.Close()
	for _, z := range edited.File {
		if z.Name == "xl/worksheets/sheet2.xml" {
			r, _ := z.Open()
			data, _ := io.ReadAll(r)
			_ = r.Close()
			if !bytes.Contains(data, marker) {
				t.Fatalf("editing Inputs!A1 removed Report!A1:B1 merged range: %s", data)
			}
			return
		}
	}
	t.Fatal("Report sheet missing")
}

func TestVerifyDelimitedConversionUsesInputSeparator(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	inCSV, outTSV := filepath.Join(dir, "in.csv"), filepath.Join(dir, "out.tsv")
	_ = os.WriteFile(inCSV, []byte("a,b\nx,y\n"), 0o600)
	sheets, err := ApplySpreadsheetEdits(inCSV, outTSV, "", map[string]string{"B2": "edited"}, nil)
	if err != nil || len(sheets) != 1 || len(sheets[0].Rows) != 2 || len(sheets[0].Rows[0]) != 2 || sheets[0].Rows[1][1] != "edited" {
		t.Fatalf("CSV to TSV lost column boundaries: err=%v rows=%+v", err, sheets)
	}
}

func TestReviewMixedXLSXNamespacePreservesWellFormedRows(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "mixed.xlsx")
	if err := WriteXLSXFile(file, []SpreadsheetSheet{{Name: "Sheet1", Rows: [][]string{{"1"}, {"2"}}}}); err != nil {
		t.Fatal(err)
	}
	orig, _ := zip.OpenReader(file)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	wsXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><x:sheetData xmlns:x="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><row r="1"><c r="A1" s="1"><v>1</v></c></row><row r="2"><c r="A2"><v>2</v></c></row></x:sheetData></worksheet>`
	for _, z := range orig.File {
		if z.Name == "xl/sharedStrings.xml" {
			continue
		}
		out, _ := zw.Create(z.Name)
		if z.Name == "xl/worksheets/sheet1.xml" {
			_, _ = out.Write([]byte(wsXML))
			continue
		}
		r, _ := z.Open()
		_, _ = io.Copy(out, r)
		_ = r.Close()
	}
	_ = orig.Close()
	_ = zw.Close()
	_ = os.WriteFile(file, buf.Bytes(), 0o600)
	sheets, editErr := ApplySpreadsheetEdits(file, file, "Sheet1", map[string]string{"A1": "3"}, nil)
	if editErr != nil || len(sheets) != 1 || sheets[0].Rows[0][0] != "3" || sheets[0].Rows[1][0] != "2" {
		t.Fatalf("unexpected edit result: err=%v sheets=%+v", editErr, sheets)
	}
	zr, _ := zip.OpenReader(file)
	defer zr.Close()
	for _, z := range zr.File {
		if z.Name == "xl/worksheets/sheet1.xml" {
			r, _ := z.Open()
			data, _ := io.ReadAll(r)
			_ = r.Close()
			for dec := xml.NewDecoder(bytes.NewReader(data)); ; {
				if _, err := dec.Token(); err != nil {
					if err == io.EOF {
						return
					}
					t.Fatalf("edit wrote malformed worksheet despite edit result %v: %v; %s", editErr, err, data)
				}
			}
		}
	}
}
