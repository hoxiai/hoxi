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
	w3, err := zw.Create("sqlite3.exe")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w3.Write([]byte("MZ-fake-sqlite-exe-binary"))
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
	sqliteRaw, err := os.ReadFile(filepath.Join(stageDir, "sqlite3.exe"))
	if err != nil || string(sqliteRaw) != "MZ-fake-sqlite-exe-binary" {
		t.Fatalf("staged sqlite3.exe mismatch: %v / %q", err, string(sqliteRaw))
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

	// resolveWindowsArchiveURL falls back to the Windows .zip asset when DownloadURL points to a .dmg or -setup.exe
	gotURL := resolveWindowsArchiveURL(&UpdateCheckResult{
		LatestVersion: "0.4.5",
		DownloadURL:   "https://github.com/hoxiai/hoxi/releases/download/v0.4.5/Hoxi-0.4.5-darwin-arm64.dmg",
	})
	wantURL := "https://github.com/hoxiai/hoxi/releases/download/v0.4.5/Hoxi-0.4.5-windows-amd64.zip"
	if gotURL != wantURL {
		t.Fatalf("resolveWindowsArchiveURL = %q, want %q", gotURL, wantURL)
	}
	gotSetupConverted := resolveWindowsArchiveURL(&UpdateCheckResult{
		LatestVersion: "0.4.5",
		DownloadURL:   "https://gitcode.com/hoxiai/hoxi/releases/download/v0.4.5/Hoxi-0.4.5-windows-amd64-setup.exe",
	})
	wantSetupConverted := "https://gitcode.com/hoxiai/hoxi/releases/download/v0.4.5/Hoxi-0.4.5-windows-amd64.zip"
	if gotSetupConverted != wantSetupConverted {
		t.Fatalf("resolveWindowsArchiveURL(-setup.exe) = %q, want %q", gotSetupConverted, wantSetupConverted)
	}
}

func TestDownloadAndVerifyDMGAndBundleResolution(t *testing.T) {
	dmgPayload := []byte("fake-dmg-payload-bytes")
	sum := sha256.Sum256(dmgPayload)
	dmgSHA := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Hoxi-0.5.0-darwin-arm64.dmg":
			w.Header().Set("Content-Type", "application/x-apple-diskimage")
			_, _ = w.Write(dmgPayload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dstPath := filepath.Join(t.TempDir(), "update.dmg")
	if err := downloadAndVerifyDMG(context.Background(), srv.Client(), srv.URL+"/Hoxi-0.5.0-darwin-arm64.dmg", dmgSHA, dstPath); err != nil {
		t.Fatalf("downloadAndVerifyDMG failed: %v", err)
	}
	gotBytes, err := os.ReadFile(dstPath)
	if err != nil || !bytes.Equal(gotBytes, dmgPayload) {
		t.Fatalf("downloaded DMG mismatch: %v / %q", err, string(gotBytes))
	}

	// Corrupt SHA-256 must fail and remove the partial file
	badPath := filepath.Join(t.TempDir(), "bad.dmg")
	err = downloadAndVerifyDMG(context.Background(), srv.Client(), srv.URL+"/Hoxi-0.5.0-darwin-arm64.dmg", strings.Repeat("f", 64), badPath)
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("expected SHA-256 mismatch error, got %v", err)
	}
	if _, statErr := os.Stat(badPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected corrupt DMG to be removed")
	}

	// resolveDarwinAppBundlePath accepts release .app bundles and rejects Hoxi Dev.app or bare binaries
	appBundle, err := resolveDarwinAppBundlePath("/Applications/Hoxi.app/Contents/MacOS/Hoxi")
	if err != nil || appBundle != "/Applications/Hoxi.app" {
		t.Fatalf("resolveDarwinAppBundlePath(/Applications/Hoxi.app/...) = %q, %v", appBundle, err)
	}
	if _, err := resolveDarwinAppBundlePath("/Users/test/build/bin/Hoxi Dev.app/Contents/MacOS/Hoxi"); err == nil {
		t.Fatalf("expected Hoxi Dev.app to be rejected by resolveDarwinAppBundlePath")
	}
	if _, err := resolveDarwinAppBundlePath("/usr/local/bin/hoxi"); err == nil {
		t.Fatalf("expected bare binary to be rejected by resolveDarwinAppBundlePath")
	}

	gotDMG := resolveDarwinDMGURL(&UpdateCheckResult{
		LatestVersion: "0.5.0",
		DownloadURL:   "https://github.com/hoxiai/hoxi/releases/download/v0.5.0/Hoxi-0.5.0-darwin-arm64.dmg",
	})
	if gotDMG != "https://github.com/hoxiai/hoxi/releases/download/v0.5.0/Hoxi-0.5.0-darwin-arm64.dmg" {
		t.Fatalf("resolveDarwinDMGURL = %q", gotDMG)
	}
}
