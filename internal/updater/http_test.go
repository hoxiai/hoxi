package updater

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNativeStatus(t *testing.T) {
	h := NewHTTPHandler(nil)
	h.SetDraining(func() bool { return true })
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/hoxi/updater/native", nil))
	var status struct {
		Available bool `json:"available"`
		Draining  bool `json:"draining"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || !status.Draining || status.Available != NativeAvailable() {
		t.Fatalf("unexpected status: %d %s", w.Code, w.Body.String())
	}
}

func TestNativeInstallRequiresConfiguration(t *testing.T) {
	if NativeAvailable() {
		t.Skip("test must not open an actual updater")
	}
	h := NewHTTPHandler(nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/hoxi/updater/install", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected status: %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/hoxi/updater/install", nil))
	if w.Code != http.StatusNotFound {
		t.Fatal("GET must never initiate installation")
	}
}
