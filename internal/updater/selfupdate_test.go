package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildTestWindowsReleaseZip(t *testing.T, includeHoxi bool) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if includeHoxi {
		w, err := zw.Create("Hoxi.exe")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("MZ-fake-hoxi-exe-binary"))
	}
	w2, err := zw.Create("opencode.exe")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w2.Write([]byte("MZ-fake-opencode-exe-binary"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:])
}

func TestDownloadAndStageWindowsZipVerifiesSHA256AndExtracts(t *testing.T) {
	zipBytes, zipSHA := buildTestWindowsReleaseZip(t, true)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Hoxi-0.4.5-windows-amd64.zip":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(zipBytes)
		case "/Hoxi-0.4.5-windows-amd64.zip.sha256":
			_, _ = w.Write([]byte(zipSHA + "  Hoxi-0.4.5-windows-amd64.zip\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	stageDir := filepath.Join(t.TempDir(), "stage")
	err := downloadAndStageWindowsZip(context.Background(), srv.Client(), srv.URL+"/Hoxi-0.4.5-windows-amd64.zip", "", stageDir)
	if err != nil {
		t.Fatalf("downloadAndStageWindowsZip failed: %v", err)
	}
	hoxiRaw, err := os.ReadFile(filepath.Join(stageDir, "Hoxi.exe"))
	if err != nil || string(hoxiRaw) != "MZ-fake-hoxi-exe-binary" {
		t.Fatalf("staged Hoxi.exe mismatch: %v / %q", err, string(hoxiRaw))
	}
	ocRaw, err := os.ReadFile(filepath.Join(stageDir, "opencode.exe"))
	if err != nil || string(ocRaw) != "MZ-fake-opencode-exe-binary" {
		t.Fatalf("staged opencode.exe mismatch: %v / %q", err, string(ocRaw))
	}
	if _, err := os.Stat(filepath.Join(stageDir, "update.zip")); !os.IsNotExist(err) {
		t.Fatalf("expected temporary update.zip to be removed after extraction")
	}

	// Corrupt SHA-256 must be rejected
	badStage := filepath.Join(t.TempDir(), "bad-stage")
	err = downloadAndStageWindowsZip(
		context.Background(),
		srv.Client(),
		srv.URL+"/Hoxi-0.4.5-windows-amd64.zip?nosidecar=1",
		strings.Repeat("0", 64),
		badStage,
	)
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("expected SHA-256 mismatch error, got %v", err)
	}
}
