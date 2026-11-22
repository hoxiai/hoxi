package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileStageIntegrityAndVideoRangeStreaming(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	root := t.TempDir()
	g := New(0, "pass", root, nil, nil)
	g.SetBrowserUIOrigins("http://127.0.0.1:5420")

	// 1. Rejects 0-byte body when expectedSize > 0 (macOS wails:// dropped File POST body)
	//    and leaves no 0-byte artifact in inputs/.
	emptyReq := httptest.NewRequest(
		http.MethodPost,
		"/api/hoxi/file/stage?directory="+url.QueryEscape(root)+"&filename=demo.mp4&expectedSize=1024",
		bytes.NewReader(nil),
	)
	emptyReq.Host = "127.0.0.1:5420"
	emptyReq.Header.Set("Origin", "wails://wails")
	emptyRec := httptest.NewRecorder()
	g.ServeHTTP(emptyRec, emptyReq)
	if emptyRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for 0-byte upload with expectedSize=1024, got %d: %s", emptyRec.Code, emptyRec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "inputs", "demo.mp4")); !os.IsNotExist(err) {
		t.Fatalf("expected no staged file on 0-byte error, stat err=%v", err)
	}

	// 2. Rejects truncated body when written != expectedSize and leaves no partial file.
	truncReq := httptest.NewRequest(
		http.MethodPost,
		"/api/hoxi/file/stage?directory="+url.QueryEscape(root)+"&filename=demo.mp4&expectedSize=100",
		bytes.NewReader([]byte("short")),
	)
	truncReq.Host = "127.0.0.1:5420"
	truncRec := httptest.NewRecorder()
	g.ServeHTTP(truncRec, truncReq)
	if truncRec.Code != http.StatusBadRequest || !strings.Contains(truncRec.Body.String(), "incomplete file upload") {
		t.Fatalf("expected 400 incomplete upload error, got %d: %s", truncRec.Code, truncRec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "inputs", "demo.mp4")); !os.IsNotExist(err) {
		t.Fatalf("expected no staged file after truncated upload, stat err=%v", err)
	}

	// 3. Rejects empty body when expectedSize is omitted.
	noSizeReq := httptest.NewRequest(
		http.MethodPost,
		"/api/hoxi/file/stage?directory="+url.QueryEscape(root)+"&filename=empty.mp4",
		bytes.NewReader(nil),
	)
	noSizeReq.Host = "127.0.0.1:5420"
	noSizeRec := httptest.NewRecorder()
	g.ServeHTTP(noSizeRec, noSizeReq)
	if noSizeRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty upload without expectedSize, got %d: %s", noSizeRec.Code, noSizeRec.Body.String())
	}

	// 4. Stages a valid .m4v file with matching expectedSize -> video/mp4 MIME and exact bytes on disk.
	payload := []byte("0123456789abcdefMP4PAYLOAD")
	okReq := httptest.NewRequest(
		http.MethodPost,
		"/api/hoxi/file/stage?directory="+url.QueryEscape(root)+"&filename=clip.m4v&expectedSize=26",
		bytes.NewReader(payload),
	)
	okReq.Host = "127.0.0.1:5420"
	okReq.Header.Set("Origin", "wails://wails")
	okRec := httptest.NewRecorder()
	g.ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusOK {
		t.Fatalf("expected 200 staging .m4v, got %d: %s", okRec.Code, okRec.Body.String())
	}
	if okRec.Header().Get("Access-Control-Allow-Origin") != "wails://wails" {
		t.Fatalf("expected CORS ACAO=wails://wails, got %q", okRec.Header().Get("Access-Control-Allow-Origin"))
	}
	var staged struct {
		OK   bool   `json:"ok"`
		Path string `json:"path"`
		Mime string `json:"mime"`
		URL  string `json:"url"`
		Size int64  `json:"size"`
	}
	if err := json.Unmarshal(okRec.Body.Bytes(), &staged); err != nil {
		t.Fatalf("decode stage response: %v", err)
	}
	if !staged.OK || staged.Size != int64(len(payload)) || staged.Mime != "video/mp4" {
		t.Fatalf("unexpected stage response: %+v", staged)
	}

	// 5. CORS preflight for Range playback from wails://wails allows Range and exposes Content-Range.
	optReq := httptest.NewRequest(http.MethodOptions, staged.URL, nil)
	optReq.Host = "127.0.0.1:5420"
	optReq.Header.Set("Origin", "wails://wails")
	optReq.Header.Set("Access-Control-Request-Headers", "range")
	optRec := httptest.NewRecorder()
	g.ServeHTTP(optRec, optReq)
	if optRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for CORS preflight, got %d", optRec.Code)
	}
	if !strings.Contains(optRec.Header().Get("Access-Control-Allow-Headers"), "Range") {
		t.Fatalf("expected Range in ACAH, got %q", optRec.Header().Get("Access-Control-Allow-Headers"))
	}
	if !strings.Contains(optRec.Header().Get("Access-Control-Expose-Headers"), "Content-Range") {
		t.Fatalf("expected Content-Range in ACEH, got %q", optRec.Header().Get("Access-Control-Expose-Headers"))
	}

	// 6. HEAD /api/hoxi/file/content?...&raw=1 returns 200 (not 502 proxy fallthrough) with video/mp4.
	headReq := httptest.NewRequest(http.MethodHead, staged.URL, nil)
	headReq.Host = "127.0.0.1:5420"
	headReq.Header.Set("Origin", "wails://wails")
	headRec := httptest.NewRecorder()
	g.ServeHTTP(headRec, headReq)
	if headRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for raw HEAD, got %d: %s", headRec.Code, headRec.Body.String())
	}
	if headRec.Header().Get("Content-Type") != "video/mp4" {
		t.Fatalf("expected video/mp4 Content-Type on HEAD, got %q", headRec.Header().Get("Content-Type"))
	}
	if headRec.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("expected Accept-Ranges: bytes on HEAD, got %q", headRec.Header().Get("Accept-Ranges"))
	}
	if headRec.Body.Len() != 0 {
		t.Fatalf("expected empty body on HEAD, got %d bytes", headRec.Body.Len())
	}

	// 7. GET with Range: bytes=0-3 returns 206 Partial Content with exact slice.
	rangeReq := httptest.NewRequest(http.MethodGet, staged.URL, nil)
	rangeReq.Host = "127.0.0.1:5420"
	rangeReq.Header.Set("Origin", "wails://wails")
	rangeReq.Header.Set("Range", "bytes=0-3")
	rangeRec := httptest.NewRecorder()
	g.ServeHTTP(rangeRec, rangeReq)
	if rangeRec.Code != http.StatusPartialContent {
		t.Fatalf("expected 206 Partial Content, got %d: %s", rangeRec.Code, rangeRec.Body.String())
	}
	if rangeRec.Body.String() != "0123" {
		t.Fatalf("expected range body 0123, got %q", rangeRec.Body.String())
	}
	if rangeRec.Header().Get("Content-Range") != "bytes 0-3/26" {
		t.Fatalf("unexpected Content-Range: %q", rangeRec.Header().Get("Content-Range"))
	}
}
