package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleSaveImagesSingleAndBatch(t *testing.T) {
	rawPNG, pngDataURL := tinyTestPNGDataURL(t)
	root := t.TempDir()
	outDir := t.TempDir()

	g := New(0, "test", root, nil, nil)
	defer g.CloseBrowser()

	// 1. Without a desktop dialog attached (headless mode), returns supported=false cleanly.
	singleBody := `{"title":"Save Share","defaultDir":` + jsonString(outDir) + `,"images":[{"filename":"session.png","dataUrl":` + jsonString(pngDataURL) + `}]}`
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodPost, "/api/hoxi/file/save-images", strings.NewReader(singleBody)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"supported":false`) {
		t.Fatalf("headless save-images = %d %s, want 200 supported=false", rec.Code, rec.Body.String())
	}

	// 2. Single-image native save writes atomically and appends .png when user omits extension.
	var seenSaveOpts SaveFileOptions
	chosenNoExt := filepath.Join(outDir, "exported-card")
	g.SetSaveFileDialog(func(opts SaveFileOptions) (string, error) {
		seenSaveOpts = opts
		return chosenNoExt, nil
	})
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodPost, "/api/hoxi/file/save-images", strings.NewReader(singleBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("single save = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if seenSaveOpts.DefaultFilename != "session.png" || seenSaveOpts.DefaultDirectory != outDir || seenSaveOpts.FilterPattern != "*.png" {
		t.Fatalf("unexpected SaveFileOptions: %+v", seenSaveOpts)
	}
	var singleResp struct {
		Supported bool     `json:"supported"`
		Saved     bool     `json:"saved"`
		Canceled  bool     `json:"canceled"`
		Paths     []string `json:"paths"`
		Directory string   `json:"directory"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &singleResp); err != nil {
		t.Fatalf("unmarshal single response: %v", err)
	}
	wantPath := chosenNoExt + ".png"
	if !singleResp.Supported || !singleResp.Saved || singleResp.Canceled || len(singleResp.Paths) != 1 || singleResp.Paths[0] != wantPath || singleResp.Directory != outDir {
		t.Fatalf("unexpected single response: %+v", singleResp)
	}
	gotBytes, err := os.ReadFile(wantPath)
	if err != nil || !bytes.Equal(gotBytes, rawPNG) {
		t.Fatalf("saved PNG mismatch: err=%v len=%d", err, len(gotBytes))
	}

	// 3. User cancel returns canceled=true and writes nothing.
	g.SetSaveFileDialog(func(SaveFileOptions) (string, error) {
		return "", nil
	})
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodPost, "/api/hoxi/file/save-images", strings.NewReader(singleBody)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"canceled":true`) || !strings.Contains(rec.Body.String(), `"saved":false`) {
		t.Fatalf("canceled save = %d %s, want canceled=true saved=false", rec.Code, rec.Body.String())
	}

	// 4. Multi-image batch save uses directory dialog and avoids overwriting existing files.
	batchDir := t.TempDir()
	existingFile := filepath.Join(batchDir, "deck-1.png")
	if err := os.WriteFile(existingFile, []byte("original-user-file"), 0o644); err != nil {
		t.Fatalf("seed existing file: %v", err)
	}
	var seenDirOpts OpenDirectoryOptions
	g.SetOpenDirectoryDialogWithOptions(func(opts OpenDirectoryOptions) (string, error) {
		seenDirOpts = opts
		return batchDir, nil
	})
	batchBody := `{"title":"Save 2 PNGs","defaultDir":` + jsonString(batchDir) + `,"images":[` +
		`{"filename":"../deck-1.png","dataUrl":` + jsonString(pngDataURL) + `},` +
		`{"filename":"deck-2.png","dataUrl":` + jsonString(pngDataURL) + `}]}`
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodPost, "/api/hoxi/file/save-images", strings.NewReader(batchBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("batch save = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if seenDirOpts.Title != "Save 2 PNGs" || seenDirOpts.DefaultDirectory != batchDir {
		t.Fatalf("unexpected dir opts: %+v", seenDirOpts)
	}
	var batchResp struct {
		Supported bool     `json:"supported"`
		Saved     bool     `json:"saved"`
		Paths     []string `json:"paths"`
		Directory string   `json:"directory"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &batchResp); err != nil {
		t.Fatalf("unmarshal batch response: %v", err)
	}
	if !batchResp.Supported || !batchResp.Saved || len(batchResp.Paths) != 2 || batchResp.Directory != batchDir {
		t.Fatalf("unexpected batch response: %+v", batchResp)
	}
	// Existing file must remain untouched.
	kept, err := os.ReadFile(existingFile)
	if err != nil || string(kept) != "original-user-file" {
		t.Fatalf("existing file was clobbered: err=%v content=%q", err, string(kept))
	}
	for _, p := range batchResp.Paths {
		if filepath.Dir(p) != batchDir || !strings.HasSuffix(p, ".png") {
			t.Fatalf("unexpected saved batch path %q", p)
		}
		b, readErr := os.ReadFile(p)
		if readErr != nil || !bytes.Equal(b, rawPNG) {
			t.Fatalf("batch file %q invalid: err=%v", p, readErr)
		}
	}

	// 5. UTF-8 CJK filename truncation preserves valid UTF-8 and dotted destinations get .png suffix.
	longCJK := strings.Repeat("架构评审与分享卡片", 15) + ".png"
	sanitized := sanitizeSavePNGFilename(longCJK, 0)
	if !strings.HasSuffix(sanitized, ".png") || len([]rune(strings.TrimSuffix(sanitized, ".png"))) > 80 {
		t.Fatalf("unexpected sanitized CJK filename: %q", sanitized)
	}
	if strings.ContainsRune(sanitized, '\uFFFD') {
		t.Fatalf("sanitized CJK filename corrupted UTF-8: %q", sanitized)
	}
	dottedDest := filepath.Join(outDir, "release-v0.7.8")
	g.SetSaveFileDialog(func(SaveFileOptions) (string, error) {
		return dottedDest, nil
	})
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodPost, "/api/hoxi/file/save-images", strings.NewReader(singleBody)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "release-v0.7.8.png") {
		t.Fatalf("dotted dest save = %d %s, want release-v0.7.8.png", rec.Code, rec.Body.String())
	}

	// 6. Duplicate names within one batch never collide even when -2 already exists in input.
	dedupDir := t.TempDir()
	allocated := allocateBatchPNGPaths(dedupDir, []string{"a.png", "a-2.png", "a.png"})
	seenAlloc := make(map[string]bool, len(allocated))
	for _, p := range allocated {
		if seenAlloc[strings.ToLower(p)] {
			t.Fatalf("duplicate path allocated in batch: %v", allocated)
		}
		seenAlloc[strings.ToLower(p)] = true
	}
}

func TestHandleSaveImagesSecurityAndRejections(t *testing.T) {
	_, pngDataURL := tinyTestPNGDataURL(t)
	root := t.TempDir()
	outDir := t.TempDir()

	g := New(0, "test", root, nil, nil)
	defer g.CloseBrowser()

	validBody := `{"images":[{"filename":"card.png","dataUrl":` + jsonString(pngDataURL) + `}]}`

	// Reject GET.
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/file/save-images", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET = %d, want 405", rec.Code)
	}

	// Reject remote tunnel allowlist & handler defense-in-depth.
	if tunnelRouteAllowed(http.MethodPost, "/api/hoxi/file/save-images") || tunnelRouteAllowed(http.MethodGet, "/api/hoxi/file/save-images") {
		t.Fatalf("/api/hoxi/file/save-images must never be in the tunnel allowlist")
	}
	rec = httptest.NewRecorder()
	g.serveRemoteRequest(rec, httptest.NewRequest(http.MethodPost, "/api/hoxi/file/save-images", strings.NewReader(validBody)), remoteSourceRelay)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("remote tunnel save-images = %d, want 403", rec.Code)
	}
	rec = httptest.NewRecorder()
	remoteReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/file/save-images", strings.NewReader(validBody))
	remoteReq = remoteReq.WithContext(context.WithValue(remoteReq.Context(), remoteSourceKey{}, remoteSourceRelay))
	g.handleSaveImages(rec, remoteReq)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("direct remote save-images = %d, want 403", rec.Code)
	}

	// Enforce strict per-boot gateway token even when Wails Host/Origin are spoofed.
	g.SetGatewayToken("save-boot-token")
	spoofed := httptest.NewRequest(http.MethodPost, "/api/hoxi/file/save-images", strings.NewReader(validBody))
	spoofed.Host = "wails"
	spoofed.Header.Set("Origin", "wails://wails")
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, spoofed)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("spoofed Wails header without token = %d %s, want 403", rec.Code, rec.Body.String())
	}
	g.SetGatewayToken("")

	// Reject non-PNG, SVG, spoofed MIME, and empty image lists.
	svgDataURL := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`))
	fakePNG := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("not a png"))
	for _, tc := range []struct {
		name string
		body string
	}{
		{"empty images", `{"images":[]}`},
		{"svg rejected", `{"images":[{"filename":"x.svg","dataUrl":` + jsonString(svgDataURL) + `}]}`},
		{"spoofed png rejected", `{"images":[{"filename":"x.png","dataUrl":` + jsonString(fakePNG) + `}]}`},
	} {
		rec = httptest.NewRecorder()
		g.ServeHTTP(rec, localRequest(http.MethodPost, "/api/hoxi/file/save-images", strings.NewReader(tc.body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: got %d %s, want 400", tc.name, rec.Code, rec.Body.String())
		}
	}

	// Reject protected destination (e.g., filesystem root or ~/.hoxi) and symlink target.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))
	if err := os.MkdirAll(filepath.Join(home, ".hoxi"), 0o755); err != nil {
		t.Fatalf("mkdir .hoxi: %v", err)
	}
	g.SetSaveFileDialog(func(SaveFileOptions) (string, error) {
		return filepath.Join(home, ".hoxi", "leak.png"), nil
	})
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodPost, "/api/hoxi/file/save-images", strings.NewReader(validBody)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("protected .hoxi destination = %d %s, want 403", rec.Code, rec.Body.String())
	}

	// Reject symlink destination even inside a normal directory.
	realTarget := filepath.Join(outDir, "real.png")
	if err := os.WriteFile(realTarget, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write real target: %v", err)
	}
	linkTarget := filepath.Join(outDir, "link.png")
	if err := os.Symlink(realTarget, linkTarget); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	g.SetSaveFileDialog(func(SaveFileOptions) (string, error) {
		return linkTarget, nil
	})
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodPost, "/api/hoxi/file/save-images", strings.NewReader(validBody)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("symlink destination = %d %s, want 400", rec.Code, rec.Body.String())
	}
}

func TestHandleSaveFileGeneralExportsAndSecurity(t *testing.T) {
	root := t.TempDir()
	outDir := t.TempDir()
	g := New(0, "test", root, nil, nil)
	defer g.CloseBrowser()

	mdContent := "# Session Notes\n\nAll good.\n"
	mdDataURL := "data:text/markdown;base64," + base64.StdEncoding.EncodeToString([]byte(mdContent))
	mdBody := `{"title":"Save Markdown","defaultDir":` + jsonString(outDir) + `,"filename":"notes-v1.2.md","dataUrl":` + jsonString(mdDataURL) + `}`

	// 1. Tunnel allowlist & remote request denied.
	if tunnelRouteAllowed(http.MethodPost, "/api/hoxi/file/save") || tunnelRouteAllowed(http.MethodGet, "/api/hoxi/file/save") {
		t.Fatalf("/api/hoxi/file/save must never be in the tunnel allowlist")
	}
	rec := httptest.NewRecorder()
	g.serveRemoteRequest(rec, httptest.NewRequest(http.MethodPost, "/api/hoxi/file/save", strings.NewReader(mdBody)), remoteSourceRelay)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("remote /api/hoxi/file/save = %d, want 403", rec.Code)
	}

	// 2. Headless returns supported=false.
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodPost, "/api/hoxi/file/save", strings.NewReader(mdBody)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"supported":false`) {
		t.Fatalf("headless save = %d %s, want supported=false", rec.Code, rec.Body.String())
	}

	// 3. Valid desktop save writes markdown atomically and appends .md when version dot present.
	destNoExt := filepath.Join(outDir, "release-v0.8")
	var seen SaveFileOptions
	g.SetSaveFileDialog(func(opts SaveFileOptions) (string, error) {
		seen = opts
		return destNoExt, nil
	})
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodPost, "/api/hoxi/file/save", strings.NewReader(mdBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("desktop save markdown = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if seen.FilterPattern != "*.md" || seen.DefaultFilename != "notes-v1.2.md" {
		t.Fatalf("unexpected SaveFileOptions: %+v", seen)
	}
	got, err := os.ReadFile(destNoExt + ".md")
	if err != nil || string(got) != mdContent {
		t.Fatalf("saved markdown mismatch: err=%v got=%q", err, string(got))
	}

	// 4. Reject executable extensions (input and chosen destination) and spoofed PNG.
	shDataURL := "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("#!/bin/sh\necho hi"))
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodPost, "/api/hoxi/file/save", strings.NewReader(`{"filename":"evil.sh","dataUrl":`+jsonString(shDataURL)+`}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("disallowed input ext = %d %s, want 400", rec.Code, rec.Body.String())
	}
	g.SetSaveFileDialog(func(SaveFileOptions) (string, error) {
		return filepath.Join(outDir, "evil.command"), nil
	})
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodPost, "/api/hoxi/file/save", strings.NewReader(mdBody)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("disallowed destination ext = %d %s, want 400", rec.Code, rec.Body.String())
	}

	// 5. Strict token enforcement against spoofed Wails headers.
	g.SetGatewayToken("save-file-boot-tok")
	spoofed := httptest.NewRequest(http.MethodPost, "/api/hoxi/file/save", strings.NewReader(mdBody))
	spoofed.Host = "wails"
	spoofed.Header.Set("Origin", "wails://wails")
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, spoofed)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("spoofed Wails header without token = %d, want 403", rec.Code)
	}
}
