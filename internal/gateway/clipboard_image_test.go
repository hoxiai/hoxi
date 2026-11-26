package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/attachment"
	"github.com/hoxiai/hoxi/internal/runner"
)

func tinyTestPNGDataURL(t *testing.T) ([]byte, string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	raw := buf.Bytes()
	return raw, "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)
}

func TestHandleClipboardImageWrite(t *testing.T) {
	rawPNG, pngDataURL := tinyTestPNGDataURL(t)
	root := t.TempDir()
	g := New(0, "test", root, nil, nil)
	defer g.CloseBrowser()

	origWriter := writeClipboardImageFn
	t.Cleanup(func() { writeClipboardImageFn = origWriter })

	var capturedBytes []byte
	var capturedPath string
	writeClipboardImageFn = func(_ context.Context, imagePath string) error {
		capturedPath = imagePath
		data, err := os.ReadFile(imagePath)
		if err != nil {
			return err
		}
		capturedBytes = data
		return nil
	}

	// Valid local POST copies image bytes and removes temporary staging file afterward.
	rec := httptest.NewRecorder()
	body := `{"dataUrl":` + jsonString(pngDataURL) + `}`
	g.ServeHTTP(rec, localRequest(http.MethodPost, "/api/hoxi/clipboard/image", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("valid clipboard image write = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(capturedBytes, rawPNG) {
		t.Fatalf("written bytes mismatch: got %d bytes, want %d bytes", len(capturedBytes), len(rawPNG))
	}
	if capturedPath == "" {
		t.Fatalf("expected temp staging path")
	}
	if _, err := os.Stat(capturedPath); !os.IsNotExist(err) {
		t.Fatalf("expected temp file %q to be removed, stat err = %v", capturedPath, err)
	}

	// Reject GET.
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/clipboard/image", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET clipboard image = %d, want 405", rec.Code)
	}

	// Reject remote request at both tunnel allowlist and handler defense-in-depth layers.
	rec = httptest.NewRecorder()
	g.serveRemoteRequest(rec, httptest.NewRequest(http.MethodPost, "/api/hoxi/clipboard/image", strings.NewReader(body)), remoteSourceRelay)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("remote tunnel clipboard image write = %d, want 403", rec.Code)
	}
	rec = httptest.NewRecorder()
	remoteReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/clipboard/image", strings.NewReader(body))
	remoteReq = remoteReq.WithContext(context.WithValue(remoteReq.Context(), remoteSourceKey{}, remoteSourceRelay))
	g.handleClipboardImageWrite(rec, remoteReq)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("remote clipboard image write = %d, want 403", rec.Code)
	}

	// Reject SVG / non-binary / invalid payloads.
	svgDataURL := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`))
	textAsPNG := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("not actually a png"))
	for _, tc := range []struct {
		name string
		body string
	}{
		{"empty body", `{}`},
		{"malformed data url", `{"dataUrl":"https://example.com/a.png"}`},
		{"svg rejected", `{"dataUrl":` + jsonString(svgDataURL) + `}`},
		{"spoofed mime rejected", `{"dataUrl":` + jsonString(textAsPNG) + `}`},
	} {
		rec = httptest.NewRecorder()
		g.ServeHTTP(rec, localRequest(http.MethodPost, "/api/hoxi/clipboard/image", strings.NewReader(tc.body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: got %d %s, want 400", tc.name, rec.Code, rec.Body.String())
		}
	}

	// Reject spoofed Wails headers when a per-boot gateway token is configured.
	g.SetGatewayToken("boot-secret-token")
	rec = httptest.NewRecorder()
	spoofedReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/clipboard/image", strings.NewReader(body))
	spoofedReq.Host = "wails"
	spoofedReq.Header.Set("Origin", "wails://wails")
	g.ServeHTTP(rec, spoofedReq)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("spoofed Wails header without token = %d %s, want 403", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	authedReq := localRequest(http.MethodPost, "/api/hoxi/clipboard/image", strings.NewReader(body))
	authedReq.Header.Set(gatewayTokenHeader, "boot-secret-token")
	g.ServeHTTP(rec, authedReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("authed local clipboard image write = %d %s, want 200", rec.Code, rec.Body.String())
	}
	g.SetGatewayToken("")

	// Propagates system clipboard failure as 500.
	writeClipboardImageFn = func(_ context.Context, _ string) error {
		return errors.New("pasteboard unavailable")
	}
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodPost, "/api/hoxi/clipboard/image", strings.NewReader(body)))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("failed writer = %d %s, want 500", rec.Code, rec.Body.String())
	}
}

func TestResolveAttachmentStorePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))

	root := t.TempDir()
	rnr := runner.NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions"))

	rawPNG, _ := tinyTestPNGDataURL(t)
	meta, err := rnr.Attachments().Save(attachment.SaveRequest{
		Filename: "diagram.png",
		Mime:     "image/png",
		Data:     rawPNG,
	})
	if err != nil {
		t.Fatalf("save attachment: %v", err)
	}

	_, wantBlobPath, err := rnr.Attachments().Get(meta.ID)
	if err != nil || wantBlobPath == "" {
		t.Fatalf("get saved attachment: %v", err)
	}

	g := New(0, "test", root, nil, nil)
	g.runner = rnr
	defer g.CloseBrowser()

	for _, input := range []string{
		meta.URL,
		"/api/hoxi/attachments/" + meta.ID,
		".hoxi/attachments/" + meta.ID + "_diagram.png",
	} {
		got := g.resolveAttachmentStorePath(input)
		if got != wantBlobPath {
			t.Fatalf("resolveAttachmentStorePath(%q) = %q, want %q", input, got, wantBlobPath)
		}
	}

	// Reject traversal or unknown IDs.
	for _, bad := range []string{
		".hoxi/attachments/../../etc/passwd",
		".hoxi/attachments/" + meta.ID + "/nested.png",
		"/api/hoxi/attachments/att_nonexistent_123456/content",
		"README.md",
	} {
		if got := g.resolveAttachmentStorePath(bad); got != "" {
			t.Fatalf("resolveAttachmentStorePath(%q) = %q, want empty", bad, got)
		}
	}
}

func TestClipboardReadAndFileOpenRequireStrictGatewayToken(t *testing.T) {
	root := t.TempDir()
	g := New(0, "test", root, nil, nil)
	defer g.CloseBrowser()
	const tok = "strict-local-boot-token"
	g.SetGatewayToken(tok)

	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/hoxi/clipboard/images", ""},
		{http.MethodPost, "/api/hoxi/file/open", `{"path":"missing.txt","directory":` + jsonString(root) + `}`},
	}

	for _, ep := range cases {
		for _, tc := range []struct {
			label  string
			host   string
			origin string
		}{
			{"plain", "", ""},
			{"spoofed-wails-origin", "", "wails://wails"},
			{"spoofed-wails-host", "wails", ""},
		} {
			var bodyReader *strings.Reader
			if ep.body != "" {
				bodyReader = strings.NewReader(ep.body)
			} else {
				bodyReader = strings.NewReader("")
			}
			req := localRequest(ep.method, ep.path, bodyReader)
			if tc.host != "" {
				req.Host = tc.host
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rec := httptest.NewRecorder()
			g.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s (%s) without token = %d, want 403: %s", ep.method, ep.path, tc.label, rec.Code, rec.Body.String())
			}
		}
	}

	// Valid X-Hoxi-Token passes the auth gate (file/open reaches 404 for missing file).
	openReq := localRequest(http.MethodPost, "/api/hoxi/file/open", strings.NewReader(`{"path":"missing.txt","directory":`+jsonString(root)+`}`))
	openReq.Header.Set(gatewayTokenHeader, tok)
	openRec := httptest.NewRecorder()
	g.ServeHTTP(openRec, openReq)
	if openRec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/hoxi/file/open with valid token = %d, want 404: %s", openRec.Code, openRec.Body.String())
	}
}
