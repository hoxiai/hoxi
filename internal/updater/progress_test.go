package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestDownloadStatusWhileResponseStillStreaming(t *testing.T) {
	defer installProgress.Store(nil)
	payload := "first chunksecond chunk"
	sum := sha256.Sum256([]byte(payload))
	resume := make(chan struct{})
	observed := make(chan InstallProgress, 1)
	finished := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/update.dmg" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("first chunk"))
		w.(http.Flusher).Flush() // Deliberately omit Content-Length.
		<-resume
		_, _ = w.Write([]byte("second chunk"))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		finished <- downloadAndVerifyDMG(ctx, srv.Client(), srv.URL+"/update.dmg", hex.EncodeToString(sum[:]), filepath.Join(t.TempDir(), "update.dmg"), func(p InstallProgress) {
			installProgress.Store(&p)
			if p.Phase == "downloading" && p.DownloadedBytes > 0 {
				select {
				case observed <- p:
				default:
				}
			}
		})
	}()
	// Always unblock and join the downloader before cleaning up global state.
	defer func() {
		close(resume)
		if err := <-finished; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-observed:
	case <-ctx.Done():
		t.Fatal("download never reported streaming progress")
	}
	h := NewHTTPHandler(nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/hoxi/updater/native", nil))
	var status struct {
		Progress *InstallProgress `json:"progress"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Progress == nil || status.Progress.Phase != "downloading" || status.Progress.DownloadedBytes != 11 || status.Progress.TotalBytes != 0 {
		t.Fatalf("status must expose actual bytes before download completes: %s", w.Body.String())
	}
}

func TestFailedInstallProgressCanBeReplacedOnRetry(t *testing.T) {
	defer installProgress.Store(nil)
	setInstallPhase("downloading")
	finishInstallProgress(errors.New("download timed out"))
	if p := installProgress.Load(); p.Phase != "failed" || p.Error != "download timed out" {
		t.Fatalf("failure must not leave a running download: %+v", p)
	}
	setInstallPhase("preparing")
	if p := installProgress.Load(); p.Phase != "preparing" || p.Error != "" {
		t.Fatalf("retry must clear the previous failure: %+v", p)
	}
}
