package opencode

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
	"sync"
	"testing"
)

func zipWithBinary(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha256Hex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// A mirror serving a tampered archive must not abort the install: the next
// candidate (here the "official" source) is tried and verified on its own.
func TestInstallFromMirrorsFallsBackOnChecksumMismatch(t *testing.T) {
	good := zipWithBinary(t, "opencode", []byte("#!/bin/sh\necho good\n"))
	bad := zipWithBinary(t, "opencode", []byte("#!/bin/sh\necho evil\n"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mirror/opencode.zip":
			_, _ = w.Write(bad)
		case "/official/opencode.zip":
			_, _ = w.Write(good)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	binDir := t.TempDir()
	urls := []string{srv.URL + "/missing/opencode.zip", srv.URL + "/mirror/opencode.zip", srv.URL + "/official/opencode.zip"}
	path, err := installFromMirrors(context.Background(), srv.Client(), urls, true, sha256Hex(good), binDir, "opencode")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(got), "good") {
		t.Fatalf("installed %q (%v), want the verified archive's binary", got, err)
	}
	assertNoTempFiles(t, binDir)
}

func TestInstallFromMirrorsFailsWhenNoArchiveVerifies(t *testing.T) {
	bad := zipWithBinary(t, "opencode", []byte("evil"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(bad) }))
	defer srv.Close()

	binDir := t.TempDir()
	_, err := installFromMirrors(context.Background(), srv.Client(), []string{srv.URL + "/a", srv.URL + "/b"}, true, strings.Repeat("0", 64), binDir, "opencode")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want checksum mismatch", err)
	}
	if _, statErr := os.Stat(filepath.Join(binDir, "opencode")); !os.IsNotExist(statErr) {
		t.Fatal("an unverified binary was installed")
	}
	assertNoTempFiles(t, binDir)
}

// Concurrent installs must each write their own temp file; the final binary
// is always complete.
func TestInstallFromMirrorsConcurrentInstallsDoNotInterleave(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 1<<20)
	archive := zipWithBinary(t, "opencode", body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archive) }))
	defer srv.Close()

	binDir := t.TempDir()
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := installFromMirrors(context.Background(), srv.Client(), []string{srv.URL + "/o.zip"}, true, sha256Hex(archive), binDir, "opencode")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(filepath.Join(binDir, "opencode"))
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("installed binary is %d bytes (%v), want %d", len(got), err, len(body))
	}
	assertNoTempFiles(t, binDir)
}

func TestExpectedArchiveSHA256RequiresChecksumForVersionOverride(t *testing.T) {
	t.Setenv("HOXI_OPENCODE_SHA256", "")
	t.Setenv("HOXI_OPENCODE_VERSION", "v9.9.9")
	_, err := expectedArchiveSHA256("darwin", "arm64")
	if err == nil || !strings.Contains(err.Error(), "HOXI_OPENCODE_SHA256") {
		t.Fatalf("err = %v, want a clear request for HOXI_OPENCODE_SHA256", err)
	}

	t.Setenv("HOXI_OPENCODE_VERSION", "v"+pinnedReleaseVersion)
	if got, err := expectedArchiveSHA256("darwin", "arm64"); err != nil || got != pinnedReleaseSHA256["darwin/arm64"] {
		t.Fatalf("pinned version override = %q, %v", got, err)
	}

	override := strings.Repeat("b", 64)
	t.Setenv("HOXI_OPENCODE_VERSION", "v9.9.9")
	t.Setenv("HOXI_OPENCODE_SHA256", override)
	if got, err := expectedArchiveSHA256("darwin", "arm64"); err != nil || got != override {
		t.Fatalf("override with checksum = %q, %v", got, err)
	}
}

func TestOfficialReleaseURLsDropUnversionedMirror(t *testing.T) {
	t.Setenv("HOXI_OPENCODE_MIRROR", "https://mirror.example/opencode/")
	urls, _, err := OfficialReleaseURLs("darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 2 || urls[0] != "https://mirror.example/opencode/v"+pinnedReleaseVersion+"/opencode-darwin-arm64.zip" {
		t.Fatalf("urls = %v, want versioned mirror then official", urls)
	}
	if !strings.HasPrefix(urls[1], "https://github.com/") {
		t.Fatalf("last candidate = %q, want the official source", urls[1])
	}
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover temp file %s", e.Name())
		}
	}
}

// Installing over an existing binary keeps the old one until the caller has
// proven the replacement runs; rollback and discard both clean up.
func TestInstalledBinaryKeepsPreviousCopyUntilDiscarded(t *testing.T) {
	archive := zipWithBinary(t, "opencode", []byte("new-binary"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archive) }))
	defer srv.Close()

	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "opencode")
	if err := os.WriteFile(binPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	installed, err := installFromMirrors(context.Background(), srv.Client(), []string{srv.URL + "/o.zip"}, true, sha256Hex(archive), binDir, "opencode")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	backup, err := os.ReadFile(BinaryBackupPath(installed))
	if err != nil || string(backup) != "old-binary" {
		t.Fatalf("backup = %q (%v), want the previous binary", backup, err)
	}

	restored, err := RestoreBinaryBackup(installed)
	if err != nil || !restored {
		t.Fatalf("restore = %v, %v; want true, nil", restored, err)
	}
	content, err := os.ReadFile(installed)
	if err != nil || string(content) != "old-binary" {
		t.Fatalf("restored binary = %q (%v)", content, err)
	}
	if restored, err := RestoreBinaryBackup(installed); restored || err != nil {
		t.Fatalf("second restore = %v, %v; want false, nil", restored, err)
	}
}

func TestDiscardBinaryBackupRemovesRollbackCopy(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "opencode")
	if err := os.WriteFile(binPath, []byte("current"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(BinaryBackupPath(binPath), []byte("previous"), 0o755); err != nil {
		t.Fatal(err)
	}
	DiscardBinaryBackup(binPath)
	if restored, _ := RestoreBinaryBackup(binPath); restored {
		t.Fatal("discarded backup must not be restorable")
	}
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("current binary was removed: %v", err)
	}
}

func TestExtractSemanticVersionAndManagedPathClassification(t *testing.T) {
	cases := map[string]string{
		"1.19.3":          "1.19.3",
		"v1.19.3":         "1.19.3",
		"opencode 1.19.3": "1.19.3",
		"OpenCode v2.0.1": "2.0.1",
		"":                "",
		"unknown":         "",
	}
	for input, want := range cases {
		if got := extractSemanticVersion(input); got != want {
			t.Fatalf("extractSemanticVersion(%q) = %q, want %q", input, got, want)
		}
	}

	hoxiHome := t.TempDir()
	t.Setenv("HOXI_HOME", hoxiHome)
	managedBin := filepath.Join(hoxiHome, "bin", "opencode")
	if !isManagedBinaryPath(managedBin) {
		t.Fatalf("expected %q to be classified as managed binary path", managedBin)
	}
	if isManagedBinaryPath("/opt/homebrew/bin/opencode") {
		t.Fatal("external path must not be classified as managed binary path")
	}
	if !isDefaultBinaryConfig("") || !isDefaultBinaryConfig("opencode") || !isDefaultBinaryConfig("opencode.exe") {
		t.Fatal("expected default binary names to be recognized")
	}
	if isDefaultBinaryConfig("/custom/path/opencode") {
		t.Fatal("explicit path must not be treated as default binary config")
	}
}
