package gateway

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestConcurrentDirectoryDialogRegistration(t *testing.T) {
	g := New(4199, "test", t.TempDir(), nil, nil)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			g.SetOpenDirectoryDialog(func() (string, error) {
				// The handler must release the read lock before invoking callbacks.
				g.SetOpenDirectoryDialog(nil)
				return "/tmp", nil
			})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			w := httptest.NewRecorder()
			g.ServeHTTP(w, localRequest(http.MethodGet, "/api/hoxi/dialog/open-directory", nil))
			if w.Code != http.StatusOK {
				t.Errorf("dialog returned %d", w.Code)
			}
		}
	}()
	wg.Wait()
}
