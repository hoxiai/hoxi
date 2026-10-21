package provider

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

const (
	relayKeyTokenPrefix = "hk-"
	relayKeyIDPrefix    = "rk_"
	relayKeyBytes       = 32
)

// RelayKeyInfo is the non-secret half of a relay key. The full token is only
// returned once, at creation time, and is stored as a SHA-256 hash.
type RelayKeyInfo struct {
	ID        string `json:"id"`
	Prefix    string `json:"prefix"`
	Label     string `json:"label,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	LastUsed  int64  `json:"lastUsed,omitempty"`
}

type relayKeyRecord struct {
	RelayKeyInfo
	Hash string `json:"hash"`
}

type relayKeyDoc struct {
	Version int              `json:"version"`
	Keys    []relayKeyRecord `json:"keys"`
}

func relayKeysPath() string {
	return hoxipaths.Join("relay-keys.json")
}

func readRelayKeyDoc(path string) relayKeyDoc {
	doc := relayKeyDoc{Version: 1}
	if path == "" {
		return doc
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return doc
	}
	var parsed relayKeyDoc
	if json.Unmarshal(raw, &parsed) != nil {
		return doc
	}
	for _, k := range parsed.Keys {
		if strings.TrimSpace(k.ID) != "" && strings.TrimSpace(k.Hash) != "" {
			doc.Keys = append(doc.Keys, k)
		}
	}
	sort.Slice(doc.Keys, func(i, j int) bool { return doc.Keys[i].CreatedAt > doc.Keys[j].CreatedAt })
	return doc
}

func writeRelayKeyDoc(path string, doc relayKeyDoc) error {
	if path == "" {
		return errors.New("relay key store is unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".relay-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(append(raw, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func hashRelayToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func relayKeyPrefix(token string) string {
	if len(token) <= 11 {
		return token
	}
	return token[:11] + "…"
}

// CreateRelayKey issues a new hk-* token. The caller receives the plaintext
// token exactly once; only its hash and display prefix are persisted.
func (s *Service) CreateRelayKey(label string) (RelayKeyInfo, string, error) {
	secret, err := randomHex(relayKeyBytes)
	if err != nil {
		return RelayKeyInfo{}, "", err
	}
	token := relayKeyTokenPrefix + secret
	idSuffix, err := randomHex(6)
	if err != nil {
		return RelayKeyInfo{}, "", err
	}
	info := RelayKeyInfo{
		ID:        relayKeyIDPrefix + idSuffix,
		Prefix:    relayKeyPrefix(token),
		Label:     strings.TrimSpace(label),
		CreatedAt: time.Now().UnixMilli(),
	}
	path := relayKeysPath()
	s.relayMu.Lock()
	defer s.relayMu.Unlock()
	doc := readRelayKeyDoc(path)
	doc.Version = 1
	doc.Keys = append(doc.Keys, relayKeyRecord{RelayKeyInfo: info, Hash: hashRelayToken(token)})
	if err := writeRelayKeyDoc(path, doc); err != nil {
		return RelayKeyInfo{}, "", err
	}
	return info, token, nil
}

// RevokeRelayKey deletes one key by id. Missing ids are not an error so a
// double click from the UI stays idempotent.
func (s *Service) RevokeRelayKey(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("key id is required")
	}
	path := relayKeysPath()
	s.relayMu.Lock()
	defer s.relayMu.Unlock()
	doc := readRelayKeyDoc(path)
	kept := doc.Keys[:0]
	removed := false
	for _, k := range doc.Keys {
		if k.ID == id {
			removed = true
			continue
		}
		kept = append(kept, k)
	}
	if !removed {
		return nil
	}
	doc.Keys = kept
	return writeRelayKeyDoc(path, doc)
}

// RelayKeys lists the stored keys without secrets.
func (s *Service) RelayKeys() []RelayKeyInfo {
	s.relayMu.Lock()
	defer s.relayMu.Unlock()
	doc := readRelayKeyDoc(relayKeysPath())
	out := make([]RelayKeyInfo, 0, len(doc.Keys))
	for _, k := range doc.Keys {
		out = append(out, k.RelayKeyInfo)
	}
	return out
}

// verifyRelayToken compares the presented token against every stored hash in
// constant time and refreshes lastUsed at most once a minute.
func (s *Service) verifyRelayToken(token string) bool {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, relayKeyTokenPrefix) {
		return false
	}
	path := relayKeysPath()
	s.relayMu.Lock()
	defer s.relayMu.Unlock()
	doc := readRelayKeyDoc(path)
	computed := hashRelayToken(token)
	matched := -1
	for i, k := range doc.Keys {
		if subtle.ConstantTimeCompare([]byte(k.Hash), []byte(computed)) == 1 {
			matched = i
		}
	}
	if matched < 0 {
		return false
	}
	now := time.Now().UnixMilli()
	if now-doc.Keys[matched].LastUsed >= 60_000 {
		doc.Keys[matched].LastUsed = now
		_ = writeRelayKeyDoc(path, doc)
	}
	return true
}

// SetRelayBaseURL records the loopback gateway origin clients should call.
func (s *Service) SetRelayBaseURL(baseURL string) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	s.relayMu.Lock()
	s.relayBaseURL = baseURL
	s.relayMu.Unlock()
}

type createRelayKeyRequest struct {
	Label string `json:"label"`
}

type relayKeyCreateResponse struct {
	Key   RelayKeyInfo `json:"key"`
	Token string       `json:"token"`
}

// handleRelayAdmin serves the desktop-only management plane for relay keys.
// It is intentionally absent from the tunnel allow-list: remote callers must
// never be able to mint a credential that reaches every configured provider.
func (s *Service) handleRelayAdmin(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.relayMu.Lock()
		base := s.relayBaseURL
		s.relayMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"baseURL": base,
			"endpoints": map[string]string{
				"chatCompletions": base + relayChatPath,
				"models":          base + relayModelsPath,
			},
			"keys": s.RelayKeys(),
		})

	case http.MethodPost:
		var req createRelayKeyRequest
		if r.Body != nil {
			_ = json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req)
		}
		info, token, err := s.CreateRelayKey(req.Label)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, relayKeyCreateResponse{Key: info, Token: token})

	case http.MethodDelete:
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			writeJSONError(w, http.StatusBadRequest, "id is required")
			return
		}
		if err := s.RevokeRelayKey(id); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "keys": s.RelayKeys()})

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "GET, POST or DELETE required")
	}
}
