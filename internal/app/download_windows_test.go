//go:build windows && production

package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// This deliberately exercises Wails' actual default WebView2 download UI. It
// supplies no custom handler and needs an operator on an unlocked desktop.
func TestWindowsWebView2DownloadsInteractive(t *testing.T) {
	if os.Getenv("HOXI_WINDOWS_INTERACTIVE_TEST") != "1" {
		t.Skip("requires an unlocked Windows desktop and an operator")
	}
	const original = "preserve this existing file"
	out := t.TempDir()
	interrupt := filepath.Join(out, "hoxi-probe-interrupted.txt")
	if err := os.WriteFile(interrupt, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		switch r.URL.Path {
		case "/attachment":
			w.Header().Set("Content-Disposition", `attachment; filename="hoxi-probe-attachment.txt"`)
			_, _ = fmt.Fprint(w, "attachment bytes")
		case "/interrupted":
			w.Header().Set("Content-Disposition", `attachment; filename="hoxi-probe-interrupted.txt"`)
			w.Header().Set("Content-Length", "4096")
			_, _ = fmt.Fprint(w, "partial bytes must not replace original")
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
			case <-time.After(8 * time.Second):
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Logf("Save the three complete downloads to %s. Try replacing the interrupted file, wait for failure, then close the window.", out)
	fixture := fmt.Sprintf(`<!doctype html><meta charset=utf-8><title>WebView2 download probe</title>
<p>Save each file to the temporary directory shown in the test terminal.</p>
<button onclick="saveBlob('hoxi-probe-blob.txt','top-level blob bytes')">Download Blob</button>
<script>function saveBlob(file, text) { const a = document.createElement('a'); a.download = file;
a.href = URL.createObjectURL(new Blob([text], {type:'text/plain'})); a.click(); }</script>
<p><iframe title="sandboxed downloads" sandbox="allow-scripts allow-downloads" srcdoc="
<button onclick=&quot;const a=document.createElement('a'); a.download='hoxi-probe-frame.txt';
a.href=URL.createObjectURL(new Blob(['sandboxed blob bytes'], {type:'text/plain'})); a.click()&quot;>Download iframe Blob</button>"></iframe></p>
<p><a href="%s/attachment">Download HTTP attachment</a></p>
<p><a href="%s/interrupted">Attempt interrupted overwrite</a></p>`, srv.URL, srv.URL)
	winOptions := buildWindowsAppOptions()
	winOptions.WebviewUserDataPath = t.TempDir()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := wails.Run(&options.App{
		Title:  "Hoxi WebView2 download probe",
		Width:  650,
		Height: 450,
		AssetServer: &assetserver.Options{
			Assets: fstest.MapFS{"index.html": {Data: []byte(fixture)}},
		},
		Windows: winOptions,
	}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"hoxi-probe-blob.txt":        "top-level blob bytes",
		"hoxi-probe-frame.txt":       "sandboxed blob bytes",
		"hoxi-probe-attachment.txt":  "attachment bytes",
		"hoxi-probe-interrupted.txt": original,
	} {
		data, err := os.ReadFile(filepath.Join(out, name))
		if err != nil || string(data) != want {
			t.Errorf("%s = %q, %v; want %q", name, data, err, want)
		}
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(strings.ToLower(entry.Name()), ".crdownload") {
			t.Errorf("download left a partial file: %s", entry.Name())
		}
	}
}
