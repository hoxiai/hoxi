package opencode

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOpenCodeResolverAndPathEnrichment(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(tmpDir, "localappdata"))
	t.Setenv("APPDATA", filepath.Join(tmpDir, "appdata"))
	EnrichSystemPath()

	dlURL, isZip, err := OfficialReleaseURL("darwin", "arm64")
	if err != nil || !isZip || !strings.HasSuffix(dlURL, "/opencode-darwin-arm64.zip") {
		t.Fatalf("unexpected darwin-arm64 release URL: url=%q isZip=%v err=%v", dlURL, isZip, err)
	}

	linuxURL, linuxZip, err := OfficialReleaseURL("linux", "amd64")
	if err != nil || linuxZip || !strings.HasSuffix(linuxURL, "/opencode-linux-x64.tar.gz") {
		t.Fatalf("unexpected linux-amd64 release URL: url=%q isZip=%v err=%v", linuxURL, linuxZip, err)
	}

	winURL, winZip, err := OfficialReleaseURL("windows", "amd64")
	if err != nil || !winZip || !strings.HasSuffix(winURL, "/opencode-windows-x64.zip") {
		t.Fatalf("unexpected windows-amd64 release URL: url=%q isZip=%v err=%v", winURL, winZip, err)
	}

	if _, _, err := OfficialReleaseURL("freebsd", "amd64"); err == nil {
		t.Fatal("expected error for unsupported OS")
	}
	if _, _, err := OfficialReleaseURL("darwin", "386"); err == nil {
		t.Fatal("expected error for unsupported architecture")
	}
}

func TestIsExecutableFileAndFindBinary(t *testing.T) {
	if isExecutableFile("") {
		t.Fatal("expected empty path to not be executable")
	}
	tmpDir := t.TempDir()
	if isExecutableFile(tmpDir) {
		t.Fatal("expected directory to not be executable")
	}
	if isExecutableFile(filepath.Join(tmpDir, "missing")) {
		t.Fatal("expected missing file to not be executable")
	}

	nonExecFile := filepath.Join(tmpDir, "nonexec")
	if err := os.WriteFile(nonExecFile, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("write nonexec file: %v", err)
	}
	if runtime.GOOS != "windows" && isExecutableFile(nonExecFile) {
		t.Fatal("expected 0644 file to not be executable on unix")
	}

	execFile := filepath.Join(tmpDir, "custom-opencode")
	script := "#!/bin/sh\necho 'opencode 1.18.0'\n"
	if err := os.WriteFile(execFile, []byte(script), 0o755); err != nil {
		t.Fatalf("write exec file: %v", err)
	}
	if !isExecutableFile(execFile) {
		t.Fatal("expected 0755 file to be executable")
	}

	// FindBinary with explicit path
	if got := FindBinary(execFile); got != execFile {
		t.Fatalf("FindBinary(%q) = %q, want %q", execFile, got, execFile)
	}

	// FindBinary via LookPath for custom command name on PATH
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LOCALAPPDATA", tmpDir)
	if got := FindBinary("custom-opencode"); got != execFile {
		t.Fatalf("FindBinary(\"custom-opencode\") = %q, want %q", got, execFile)
	}
	_ = FindBinary("opencode")

	// ResolveOrInstall when binary exists locally
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	resolved, err := ResolveOrInstall(context.Background(), execFile, logger)
	if err != nil {
		t.Fatalf("ResolveOrInstall unexpected error: %v", err)
	}
	if resolved != execFile {
		t.Fatalf("ResolveOrInstall = %q, want %q", resolved, execFile)
	}
}

func TestResolveVersionAndCaching(t *testing.T) {
	if resolveVersionTimeout < 8*time.Second {
		t.Fatalf("resolveVersionTimeout = %s, want >= 8s for Bun cold starts", resolveVersionTimeout)
	}
	if got := ResolveVersion(""); got != "" {
		t.Fatalf("expected empty version for empty path, got %q", got)
	}

	tmpDir := t.TempDir()
	badBin := filepath.Join(tmpDir, "bad-opencode")
	if err := os.WriteFile(badBin, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write bad script: %v", err)
	}
	if got := ResolveVersion(badBin); got != "" {
		t.Fatalf("expected empty version for failing binary, got %q", got)
	}

	verBin := filepath.Join(tmpDir, "fake-opencode")
	script := "#!/bin/sh\necho 'opencode 1.18.0'\necho 'extra line'\n"
	if err := os.WriteFile(verBin, []byte(script), 0o755); err != nil {
		t.Fatalf("write version script: %v", err)
	}

	secondBin := filepath.Join(tmpDir, "fake-opencode-2")
	countFile := filepath.Join(tmpDir, "invocations.log")
	secondScript := "#!/bin/sh\necho x >> '" + countFile + "'\necho 'opencode 1.19.0'\n"
	if err := os.WriteFile(secondBin, []byte(secondScript), 0o755); err != nil {
		t.Fatalf("write second version script: %v", err)
	}

	got := ResolveVersion(verBin)
	if got != "opencode 1.18.0" {
		t.Fatalf("ResolveVersion = %q, want %q", got, "opencode 1.18.0")
	}

	// Concurrent calls to the second binary should coalesce and populate its own cache slot
	// without evicting the first binary's cached version.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v := ResolveVersion(secondBin); v != "opencode 1.19.0" {
				t.Errorf("concurrent ResolveVersion(secondBin) = %q, want opencode 1.19.0", v)
			}
		}()
	}
	wg.Wait()

	// Remove both underlying scripts and verify both cached versions remain intact when alternating.
	if err := os.Remove(verBin); err != nil {
		t.Fatalf("remove script: %v", err)
	}
	if err := os.Remove(secondBin); err != nil {
		t.Fatalf("remove second script: %v", err)
	}
	if cached := ResolveVersion(verBin); cached != "opencode 1.18.0" {
		t.Fatalf("expected cached version %q, got %q", "opencode 1.18.0", cached)
	}
	if cached2 := ResolveVersion(secondBin); cached2 != "opencode 1.19.0" {
		t.Fatalf("expected second cached version %q, got %q", "opencode 1.19.0", cached2)
	}
	if raw, err := os.ReadFile(countFile); err != nil || strings.Count(string(raw), "x") != 1 {
		t.Fatalf("expected singleflight to invoke second binary once, got %q (err=%v)", string(raw), err)
	}
}

func TestExtractBinaryFromZip(t *testing.T) {
	if _, err := extractBinaryFromZip([]byte("not-a-zip"), "opencode"); err == nil {
		t.Fatal("expected error for invalid zip data")
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.Create("nested/opencode")
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	wantPayload := []byte("fake-opencode-binary-payload")
	if _, err := fw.Write(wantPayload); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}

	got, err := extractBinaryFromZip(buf.Bytes(), "opencode")
	if err != nil {
		t.Fatalf("extractBinaryFromZip unexpected error: %v", err)
	}
	if !bytes.Equal(got, wantPayload) {
		t.Fatalf("extracted payload = %q, want %q", string(got), string(wantPayload))
	}

	if _, err := extractBinaryFromZip(buf.Bytes(), "missing-bin"); err == nil {
		t.Fatal("expected error when binary is missing from zip archive")
	}
}

func TestExtractBinaryFromTarGz(t *testing.T) {
	if _, err := extractBinaryFromTarGz([]byte("not-a-targz"), "opencode"); err == nil {
		t.Fatal("expected error for invalid tar.gz data")
	}

	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	wantPayload := []byte("fake-targz-opencode-binary")
	hdr := &tar.Header{
		Name:     "release/opencode",
		Mode:     0o755,
		Size:     int64(len(wantPayload)),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("tar write header: %v", err)
	}
	if _, err := tw.Write(wantPayload); err != nil {
		t.Fatalf("tar write payload: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	got, err := extractBinaryFromTarGz(buf.Bytes(), "opencode")
	if err != nil {
		t.Fatalf("extractBinaryFromTarGz unexpected error: %v", err)
	}
	if !bytes.Equal(got, wantPayload) {
		t.Fatalf("extracted payload = %q, want %q", string(got), string(wantPayload))
	}

	if _, err := extractBinaryFromTarGz(buf.Bytes(), "missing-bin"); err == nil {
		t.Fatal("expected error when binary is missing from tar.gz archive")
	}
}

func TestWaitForHealthAndStartDaemonWithBin(t *testing.T) {
	const testPassword = "test-secret-password"

	healthy := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "opencode" || pass != testPassword {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/global/health" && healthy {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"healthy":true}`))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	port := srv.Listener.Addr().(*net.TCPAddr).Port

	// 1. Timeout when unhealthy
	if err := WaitForHealth(context.Background(), port, testPassword, 150*time.Millisecond); err == nil {
		t.Fatal("expected WaitForHealth timeout error when server returns 503")
	}

	// 2. Success when healthy and Basic Auth matches
	healthy = true
	if err := WaitForHealth(context.Background(), port, testPassword, 2*time.Second); err != nil {
		t.Fatalf("WaitForHealth unexpected error: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()

	// 3. StartDaemonWithBin error when binary does not exist
	if _, err := StartDaemonWithBin(context.Background(), filepath.Join(tmpDir, "nonexistent"), tmpDir, "", port, testPassword, logger); err == nil {
		t.Fatal("expected error when starting nonexistent daemon binary")
	}

	// 4. StartDaemonWithBin error path when health check fails / context expires
	healthy = false
	fakeDaemon := filepath.Join(tmpDir, "fake-daemon")
	if err := os.WriteFile(fakeDaemon, []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatalf("write fake daemon: %v", err)
	}
	shortCtx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := StartDaemonWithBin(shortCtx, fakeDaemon, tmpDir, filepath.Join(tmpDir, "opencode.json"), port, testPassword, logger); err == nil {
		t.Fatal("expected StartDaemonWithBin to fail when health check times out")
	}

	// 5. StartDaemonWithBin success path when health endpoint responds 200 OK
	healthy = true
	child, err := StartDaemonWithBin(context.Background(), fakeDaemon, tmpDir, filepath.Join(tmpDir, "opencode.json"), port, testPassword, logger)
	if err != nil {
		t.Fatalf("StartDaemonWithBin unexpected error: %v", err)
	}
	if child != nil && child.Process != nil {
		_ = child.Process.Kill()
		_ = child.Wait()
	}
}

func TestReapOrphanedDaemons(t *testing.T) {
	ReapOrphanedDaemons(nil)
}

func TestEnsureHoxiEngineDataDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOXI_ENGINE_DATA_DIR", "")
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", "")

	if gotDB := ResolveHoxiDBPath(); gotDB != filepath.Join(home, ".hoxi", "hoxi.db") {
		t.Fatalf("ResolveHoxiDBPath() = %s, want %s", gotDB, filepath.Join(home, ".hoxi", "hoxi.db"))
	}

	extOpenCodeDir := filepath.Join(home, ".local", "share", "opencode")
	if err := os.MkdirAll(extOpenCodeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	wantAuth := []byte(`{"openai":{"key":"sk-test"}}`)
	if err := os.WriteFile(filepath.Join(extOpenCodeDir, "auth.json"), wantAuth, 0o600); err != nil {
		t.Fatal(err)
	}

	dataDir, stateDir := EnsureHoxiEngineDataDir()
	if dataDir != filepath.Join(home, ".hoxi", "engine-data") || stateDir != filepath.Join(home, ".hoxi", "engine-state") {
		t.Fatalf("unexpected engine dirs: data=%s state=%s", dataDir, stateDir)
	}
	seededAuth, err := os.ReadFile(filepath.Join(dataDir, "opencode", "auth.json"))
	if err != nil || !bytes.Equal(seededAuth, wantAuth) {
		t.Fatalf("expected seeded auth.json, got %q (err=%v)", string(seededAuth), err)
	}
}
