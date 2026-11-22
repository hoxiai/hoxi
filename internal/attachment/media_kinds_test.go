package attachment

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMediaM4VAndHeadContentSupport(t *testing.T) {
	if got := DetectFileMimeType("demo.m4v", nil); got != "video/mp4" {
		t.Fatalf("DetectFileMimeType(.m4v) = %q, want video/mp4", got)
	}
	kind, mime, err := ClassifyFile("demo.m4v", "video/x-m4v")
	if err != nil || kind != KindVideo || mime != "video/mp4" {
		t.Fatalf("ClassifyFile(.m4v, video/x-m4v) = (%q, %q, %v), want (video, video/mp4, nil)", kind, mime, err)
	}

	store := NewStore(t.TempDir())
	mux := http.NewServeMux()
	store.RegisterRoutes(mux)

	meta, err := store.Save(SaveRequest{
		SessionID: "ses_media_head",
		Filename:  "clip.m4v",
		Mime:      "video/x-m4v",
		Data:      []byte("0123456789abcdef"),
	})
	if err != nil {
		t.Fatalf("Save(.m4v) failed: %v", err)
	}
	if meta.Mime != "video/mp4" {
		t.Fatalf("saved mime = %q, want video/mp4", meta.Mime)
	}

	headRec := httptest.NewRecorder()
	mux.ServeHTTP(headRec, httptest.NewRequest(http.MethodHead, meta.URL, nil))
	if headRec.Code != http.StatusOK {
		t.Fatalf("HEAD attachment content status = %d: %s", headRec.Code, headRec.Body.String())
	}
	if headRec.Header().Get("Content-Type") != "video/mp4" {
		t.Fatalf("HEAD Content-Type = %q, want video/mp4", headRec.Header().Get("Content-Type"))
	}
	if headRec.Body.Len() != 0 {
		t.Fatalf("HEAD body len = %d, want 0", headRec.Body.Len())
	}
}
