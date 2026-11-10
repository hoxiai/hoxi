package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/attachment"
)

// decodeSensitiveRequest builds an absolute path inside a synthetic credential
// store so isSensitiveCredentialPath matches it.
func sensitiveFixture(t *testing.T) (home, root, secret string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))
	root = t.TempDir()
	secret = writeTestFile(t, filepath.Join(home, ".gemini", "oauth_creds.json"), "SYNTHETIC_CREDENTIAL\n")
	return home, root, secret
}

func TestSensitiveFileReadAsksDesktopAndNeverRemembers(t *testing.T) {
	_, root, secret := sensitiveFixture(t)
	g := New(0, "test", root, nil, nil)
	defer g.CloseBrowser()

	query := "?" + url.Values{"path": {secret}, "directory": {root}}.Encode()

	// Headless / no dialog: fail closed.
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/file/content"+query, nil))
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "SYNTHETIC_CREDENTIAL") {
		t.Fatalf("read without a dialog = %d %s, want 403", rec.Code, rec.Body.String())
	}

	// Declining the dialog denies the read.
	calls := 0
	g.SetFileAccessConfirmation(func(_ context.Context, c FileAccessConfirmation) (bool, error) {
		calls++
		if c.Action != "read" || c.Path != secret {
			t.Fatalf("unexpected confirmation %+v", c)
		}
		return false, nil
	})
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/file/content"+query, nil))
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "SYNTHETIC_CREDENTIAL") {
		t.Fatalf("declined read = %d %s, want 403", rec.Code, rec.Body.String())
	}
	if calls != 1 {
		t.Fatalf("confirmation calls = %d, want 1", calls)
	}

	// Accepting grants that one read only.
	g.SetFileAccessConfirmation(func(_ context.Context, _ FileAccessConfirmation) (bool, error) {
		calls++
		return true, nil
	})
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/file/content"+query, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "SYNTHETIC_CREDENTIAL") {
		t.Fatalf("accepted read = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if calls != 2 {
		t.Fatalf("accepted read should ask again: calls = %d, want 2", calls)
	}
}

func TestSensitiveFileOpenAsksDesktop(t *testing.T) {
	_, root, secret := sensitiveFixture(t)
	g := New(0, "test", root, nil, nil)
	defer g.CloseBrowser()

	body := `{"path":` + jsonString(secret) + `,"directory":` + jsonString(root) + `,"action":"reveal"}`

	// No dialog: fail closed.
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodPost, "/api/hoxi/file/open", strings.NewReader(body)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("open without a dialog = %d %s, want 403", rec.Code, rec.Body.String())
	}

	// Declining denies the open.
	g.SetFileAccessConfirmation(func(_ context.Context, c FileAccessConfirmation) (bool, error) {
		if c.Action != "reveal" || c.Path != secret {
			t.Fatalf("unexpected confirmation %+v", c)
		}
		return false, nil
	})
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodPost, "/api/hoxi/file/open", strings.NewReader(body)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("declined open = %d %s, want 403", rec.Code, rec.Body.String())
	}
}

func TestSensitiveFileAccessNeverOfferedToRemoteControllers(t *testing.T) {
	home, root, secret := sensitiveFixture(t)
	_ = home
	g := New(0, "test", root, nil, nil)
	defer g.CloseBrowser()

	calls := 0
	g.SetFileAccessConfirmation(func(_ context.Context, _ FileAccessConfirmation) (bool, error) {
		calls++
		return true, nil
	})

	query := "?" + url.Values{"path": {secret}, "directory": {root}}.Encode()
	rec := httptest.NewRecorder()
	g.serveTunnelRequest(rec, localRequest(http.MethodGet, "/api/hoxi/file/content"+query, nil))
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "SYNTHETIC_CREDENTIAL") {
		t.Fatalf("remote sensitive read = %d %s, want 403 without content", rec.Code, rec.Body.String())
	}
	if calls != 0 {
		t.Fatalf("remote request must never raise a desktop dialog, got %d", calls)
	}
}

func TestOutOfWorkspaceFileReadSkipsSensitiveDialog(t *testing.T) {
	home, root, _ := sensitiveFixture(t)
	ordinary := writeTestFile(t, filepath.Join(home, "notes.md"), "ordinary notes\n")
	g := New(0, "test", root, nil, nil)
	defer g.CloseBrowser()

	calls := 0
	g.SetFileAccessConfirmation(func(_ context.Context, _ FileAccessConfirmation) (bool, error) {
		calls++
		return false, nil
	})

	query := "?" + url.Values{"path": {ordinary}, "directory": {root}}.Encode()
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/file/content"+query, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ordinary notes") {
		t.Fatalf("desktop non-sensitive read = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if calls != 0 {
		t.Fatalf("non-sensitive path must not ask, got %d calls", calls)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestFileContentSpreadsheetSheetsAndPDFExtract(t *testing.T) {
	root := t.TempDir()
	g := New(0, "test", root, nil, nil)
	defer g.CloseBrowser()

	xlsxPath := filepath.Join(root, "metrics.xlsx")
	if err := attachment.WriteXLSXFile(xlsxPath, []attachment.SpreadsheetSheet{
		{
			Name: "ROAS分析",
			Rows: [][]string{
				{"渠道", "消耗", "GMV"},
				{"Meta", "1200", "4800"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	q := "?" + url.Values{"path": {"metrics.xlsx"}, "directory": {root}}.Encode()
	g.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/file/content"+q, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET metrics.xlsx = %d: %s", rec.Code, rec.Body.String())
	}
	var xlsxResp struct {
		MimeType      string                        `json:"mimeType"`
		ExtractedText string                        `json:"extractedText"`
		Sheets        []attachment.SpreadsheetSheet `json:"sheets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &xlsxResp); err != nil {
		t.Fatal(err)
	}
	if len(xlsxResp.Sheets) != 1 || xlsxResp.Sheets[0].Name != "ROAS分析" || len(xlsxResp.Sheets[0].Rows) != 2 {
		t.Fatalf("unexpected sheets in /file/content response: %+v", xlsxResp.Sheets)
	}
	if !strings.Contains(xlsxResp.ExtractedText, "Meta\t1200\t4800") {
		t.Fatalf("unexpected extractedText: %q", xlsxResp.ExtractedText)
	}

	pdfPath := filepath.Join(root, "contract.pdf")
	pdfBytes := []byte("%PDF-1.4\n1 0 obj\n<< /Length 44 >>\nstream\nBT\n(Service Level Agreement) Tj\nET\nendstream\nendobj\n%%EOF\n")
	if err := os.WriteFile(pdfPath, pdfBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	recPDF := httptest.NewRecorder()
	qPDF := "?" + url.Values{"path": {"contract.pdf"}, "directory": {root}}.Encode()
	g.ServeHTTP(recPDF, localRequest(http.MethodGet, "/api/hoxi/file/content"+qPDF, nil))
	if recPDF.Code != http.StatusOK {
		t.Fatalf("GET contract.pdf = %d: %s", recPDF.Code, recPDF.Body.String())
	}
	var pdfResp struct {
		Type          string `json:"type"`
		MimeType      string `json:"mimeType"`
		Content       string `json:"content"`
		ExtractedText string `json:"extractedText"`
	}
	if err := json.Unmarshal(recPDF.Body.Bytes(), &pdfResp); err != nil {
		t.Fatal(err)
	}
	if pdfResp.MimeType != "application/pdf" || pdfResp.Content == "" || !strings.Contains(pdfResp.ExtractedText, "Service Level Agreement") {
		t.Fatalf("unexpected pdf response: %+v", pdfResp)
	}

	// Inline spreadsheet cell edit (/api/hoxi/file/spreadsheet-edit)
	editBody, _ := json.Marshal(map[string]any{
		"path":      "metrics.xlsx",
		"directory": root,
		"sheet":     "ROAS分析",
		"edits":     map[string]string{"C2": "6400"},
	})
	recEdit := httptest.NewRecorder()
	g.ServeHTTP(recEdit, localRequest(http.MethodPost, "/api/hoxi/file/spreadsheet-edit", strings.NewReader(string(editBody))))
	if recEdit.Code != http.StatusOK {
		t.Fatalf("POST /api/hoxi/file/spreadsheet-edit = %d: %s", recEdit.Code, recEdit.Body.String())
	}
	var editResp struct {
		OK     bool                          `json:"ok"`
		Sheets []attachment.SpreadsheetSheet `json:"sheets"`
	}
	if err := json.Unmarshal(recEdit.Body.Bytes(), &editResp); err != nil {
		t.Fatal(err)
	}
	if !editResp.OK || len(editResp.Sheets) != 1 || editResp.Sheets[0].Rows[1][2] != "6400" {
		t.Fatalf("unexpected edited sheets: %+v", editResp.Sheets)
	}

	// Rejects remote spreadsheet edit and protected path edit
	recRemote := httptest.NewRecorder()
	g.serveTunnelRequest(recRemote, localRequest(http.MethodPost, "/api/hoxi/file/spreadsheet-edit", strings.NewReader(string(editBody))))
	if recRemote.Code != http.StatusForbidden {
		t.Fatalf("remote POST /api/hoxi/file/spreadsheet-edit = %d, want 403", recRemote.Code)
	}
	protectedBody, _ := json.Marshal(map[string]any{
		"path":      ".hoxi/cloudflared.json",
		"directory": root,
		"edits":     map[string]string{"A1": "x"},
	})
	recProt := httptest.NewRecorder()
	g.ServeHTTP(recProt, localRequest(http.MethodPost, "/api/hoxi/file/spreadsheet-edit", strings.NewReader(string(protectedBody))))
	if recProt.Code != http.StatusForbidden {
		t.Fatalf("protected POST /api/hoxi/file/spreadsheet-edit = %d, want 403", recProt.Code)
	}
}
