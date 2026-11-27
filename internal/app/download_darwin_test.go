//go:build darwin && cgo

package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSanitizeNativeDownloadFilename(t *testing.T) {
	cases := []struct {
		name      string
		suggested string
		resp      string
		mime      string
		want      string
	}{
		{
			name:      "preserves custom qp project extension",
			suggested: "editor-project-2026-11-27T12-00-00.qp",
			mime:      "application/json",
			want:      "editor-project-2026-11-27T12-00-00.qp",
		},
		{
			name:      "preserves png export name",
			suggested: "image-export-2026-11-27T12-00-00.png",
			mime:      "image/png",
			want:      "image-export-2026-11-27T12-00-00.png",
		},
		{
			name:      "preserves psd export name",
			suggested: "image-export-2026-11-27T12-00-00.psd",
			mime:      "image/vnd.adobe.photoshop",
			want:      "image-export-2026-11-27T12-00-00.psd",
		},
		{
			name:      "strips posix traversal",
			suggested: "../../etc/evil.qp",
			mime:      "application/json",
			want:      "evil.qp",
		},
		{
			name:      "strips windows traversal",
			suggested: `..\..\secret\img.png`,
			mime:      "image/png",
			want:      "img.png",
		},
		{
			name:      "strips leading dots and control chars",
			suggested: "...hidden\nname:1.qp",
			mime:      "application/json",
			want:      "hidden-name-1.qp",
		},
		{
			name:      "falls back to response suggested filename when generic",
			suggested: "unknown",
			resp:      "SKU-88-主图1-精修v1.png",
			mime:      "image/png",
			want:      "SKU-88-主图1-精修v1.png",
		},
		{
			name:      "appends extension from mime when missing",
			suggested: "retouched-image",
			mime:      "image/png; charset=binary",
			want:      "retouched-image.png",
		},
		{
			name:      "empty name falls back to download with mime ext",
			suggested: "",
			resp:      "",
			mime:      "image/jpeg",
			want:      "download.jpg",
		},
		{
			name:      "empty name without known mime falls back to download",
			suggested: "...",
			resp:      "",
			mime:      "application/octet-stream",
			want:      "download",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeNativeDownloadFilename(tc.suggested, tc.resp, tc.mime)
			if got != tc.want {
				t.Fatalf("sanitizeNativeDownloadFilename(%q, %q, %q) = %q, want %q", tc.suggested, tc.resp, tc.mime, got, tc.want)
			}
		})
	}
}

func TestIsAttachmentContentDisposition(t *testing.T) {
	cases := []struct {
		header string
		want   bool
	}{
		{header: "attachment", want: true},
		{header: "Attachment; filename=\"report.csv\"", want: true},
		{header: "  attachment ; filename*=UTF-8''demo.qp", want: true},
		{header: "inline; filename=\"preview.png\"", want: false},
		{header: "attachments", want: false},
		{header: "", want: false},
	}

	for _, tc := range cases {
		if got := isAttachmentContentDisposition(tc.header); got != tc.want {
			t.Fatalf("isAttachmentContentDisposition(%q) = %v, want %v", tc.header, got, tc.want)
		}
	}
}

func TestNativeWKWebViewDownloadsAcrossIframes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live WKWebView probe in -short mode")
	}
	// Cross-origin iframe server simulating an embedded image editor inside WKWebView.
	studioSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><body><script>
			function triggerBlob(text, mime, name) {
				const url = URL.createObjectURL(new Blob([text], { type: mime }));
				const a = document.createElement('a');
				a.href = url;
				a.download = name;
				a.style.display = 'none';
				document.body.appendChild(a);
				a.click();
				a.remove();
				setTimeout(() => URL.revokeObjectURL(url), 60000);
			}
			window.addEventListener('load', () => {
				triggerBlob('{"format":"editor-project","version":1}', 'application/json', 'project-demo.qp');
				setTimeout(() => {
					triggerBlob('PNG_BYTES_DEMO', 'image/png', 'retouched-demo.png');
				}, 250);
				setTimeout(() => {
					// Detached data: URL anchor matching legacy raster export behavior.
					const a = document.createElement('a');
					a.href = 'data:image/png;base64,TEVHQUNZX0RBVEFfREVNTw==';
					a.download = 'legacy-data.png';
					a.click();
				}, 450);
			});
		</script></body></html>`))
	}))
	defer studioSrv.Close()

	// Sandboxed iframe server simulating a web tab with allow-downloads.
	workAppSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><body><script>
			window.addEventListener('load', () => {
				setTimeout(() => {
					const url = URL.createObjectURL(new Blob(['SANDBOX_IMAGE_DEMO'], { type: 'image/png' }));
					const a = document.createElement('a');
					a.href = url;
					a.download = 'sandbox-demo.png';
					a.style.display = 'none';
					document.body.appendChild(a);
					a.click();
					a.remove();
					setTimeout(() => URL.revokeObjectURL(url), 60000);
				}, 500);
			});
		</script></body></html>`))
	}))
	defer workAppSrv.Close()

	hostSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<!doctype html><html><body>
			<iframe src="%s"></iframe>
			<iframe sandbox="allow-scripts allow-forms allow-downloads" src="%s"></iframe>
		</body></html>`, studioSrv.URL, workAppSrv.URL)
	}))
	defer hostSrv.Close()

	outDir := t.TempDir()
	binary := filepath.Join(t.TempDir(), "webview-download-probe")
	compile := exec.Command("clang", "-fblocks", "-framework", "Cocoa", "-framework", "WebKit", "testdata/webview_download_probe.m", "-o", binary)
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile WKWebView download probe: %v\n%s", err, out)
	}
	if out, err := exec.Command(binary, hostSrv.URL, outDir).CombinedOutput(); err != nil {
		t.Fatalf("WKWebView download probe failed: %v\n%s", err, out)
	}

	expected := map[string]string{
		"project-demo.qp":    `{"format":"editor-project","version":1}`,
		"retouched-demo.png": "PNG_BYTES_DEMO",
		"legacy-data.png":    "LEGACY_DATA_DEMO",
		"sandbox-demo.png":   "SANDBOX_IMAGE_DEMO",
	}
	for name, want := range expected {
		data, err := os.ReadFile(filepath.Join(outDir, name))
		if err != nil {
			t.Fatalf("expected downloaded file %q to exist: %v", name, err)
		}
		if string(data) != want {
			t.Fatalf("downloaded file %q = %q, want %q", name, string(data), want)
		}
	}
}
