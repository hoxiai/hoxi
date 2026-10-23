package pairing

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// RegisterRoutes mounts the desktop-only pairing API. It is deliberately
// absent from the gateway's tunnel allow-list, so the phone can never read
// this desktop's LAN pairing token remotely.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/hoxi/pairing/qr", s.handleQR)
	mux.HandleFunc("/api/hoxi/pairing/rotate", s.handleRotate)
}

func (s *Service) handleQR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	code, err := s.Code()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not build pairing code")
		return
	}
	writeJSON(w, http.StatusOK, code)
}

func (s *Service) handleRotate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	code, err := s.Rotate()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not rotate the pairing token")
		return
	}
	writeJSON(w, http.StatusOK, code)
}

// Authorize reports whether the request carries this desktop's LAN pairing
// token in the X-Auth header or as an Authorization: Bearer value, compared in
// constant time.
func (s *Service) Authorize(r *http.Request) bool {
	return s.authorizeWith(r, s.Token)
}

// AuthorizeRelay reports whether the request carries this desktop's relay
// pairing token. Keeping the relay token distinct from the LAN token ensures a
// token exposed over cleartext LAN probing cannot authorize public relay calls.
func (s *Service) AuthorizeRelay(r *http.Request) bool {
	return s.authorizeWith(r, s.RelayToken)
}

func (s *Service) authorizeWith(r *http.Request, wantFn func() (string, error)) bool {
	if r == nil {
		return false
	}
	got := tokenFromRequest(r)
	if got == "" {
		return false
	}
	want, err := wantFn()
	if err != nil || want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func tokenFromRequest(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Auth")); v != "" {
		return v
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
