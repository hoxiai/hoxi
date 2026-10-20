package attachment

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

const (
	// MaxAttachmentBytes is the maximum allowed raw file size (15 MB).
	MaxAttachmentBytes = 15 * 1024 * 1024
)

type Kind string

const (
	KindImage       Kind = "image"
	KindPDF         Kind = "pdf"
	KindSpreadsheet Kind = "spreadsheet"
	KindDocument    Kind = "document"
	KindText        Kind = "text"
)

type Metadata struct {
	ID          string `json:"id"`
	SessionID   string `json:"sessionID,omitempty"`
	Directory   string `json:"directory,omitempty"`
	Filename    string `json:"filename"`
	Mime        string `json:"mime"`
	Size        int64  `json:"size"`
	Kind        Kind   `json:"kind"`
	URL         string `json:"url"`
	TextPreview string `json:"textPreview,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
}

type SaveRequest struct {
	SessionID string `json:"sessionID,omitempty"`
	Directory string `json:"directory,omitempty"`
	Filename  string `json:"filename"`
	Mime      string `json:"mime"`
	DataURL   string `json:"dataUrl,omitempty"`
	Data      []byte `json:"-"`
}

type Store struct {
	mu      sync.RWMutex
	baseDir string
}

func defaultBaseDir() string {
	if dir := hoxipaths.Join("attachments"); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), "hoxi-attachments")
}

func NewStore(baseDir string) *Store {
	dir := strings.TrimSpace(baseDir)
	if dir == "" {
		dir = defaultBaseDir()
	}
	_ = os.MkdirAll(dir, 0o700)
	return &Store{baseDir: dir}
}

func newAttachmentID(now int64) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("att_%d_%s", now, hex.EncodeToString(b[:]))
}

func isValidAttachmentID(id string) bool {
	if !strings.HasPrefix(id, "att_") || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
			return false
		}
	}
	return true
}

func ClassifyFile(filename, mimeType string) (Kind, string, error) {
	lowerMime := strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(filename)))

	if lowerMime == "image/jpg" {
		lowerMime = "image/jpeg"
	}
	switch lowerMime {
	case "image/png", "image/jpeg", "image/webp", "image/gif", "image/bmp", "image/tiff", "image/svg+xml", "image/heic", "image/heif", "image/avif":
		return KindImage, lowerMime, nil
	case "application/pdf":
		return KindPDF, "application/pdf", nil
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return KindSpreadsheet, lowerMime, nil
	case "application/vnd.ms-excel", "application/excel", "application/x-excel", "application/x-msexcel":
		return KindSpreadsheet, "application/vnd.ms-excel", nil
	case "application/vnd.oasis.opendocument.spreadsheet":
		return KindSpreadsheet, lowerMime, nil
	case "text/csv", "application/csv":
		return KindSpreadsheet, "text/csv", nil
	case "text/tab-separated-values":
		return KindSpreadsheet, "text/tab-separated-values", nil
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return KindDocument, lowerMime, nil
	case "application/msword":
		return KindDocument, "application/msword", nil
	case "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return KindDocument, lowerMime, nil
	case "application/vnd.ms-powerpoint":
		return KindDocument, "application/vnd.ms-powerpoint", nil
	case "application/rtf", "text/rtf":
		return KindDocument, "application/rtf", nil
	}

	switch ext {
	case ".png":
		return KindImage, "image/png", nil
	case ".jpg", ".jpeg":
		return KindImage, "image/jpeg", nil
	case ".webp":
		return KindImage, "image/webp", nil
	case ".gif":
		return KindImage, "image/gif", nil
	case ".svg":
		return KindImage, "image/svg+xml", nil
	case ".bmp", ".tif", ".tiff", ".heic", ".heif", ".avif":
		return KindImage, firstNonEmpty(lowerMime, "image/png"), nil
	case ".pdf":
		return KindPDF, "application/pdf", nil
	case ".xlsx":
		return KindSpreadsheet, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", nil
	case ".xls":
		return KindSpreadsheet, "application/vnd.ms-excel", nil
	case ".ods":
		return KindSpreadsheet, "application/vnd.oasis.opendocument.spreadsheet", nil
	case ".csv":
		return KindSpreadsheet, "text/csv", nil
	case ".tsv":
		return KindSpreadsheet, "text/tab-separated-values", nil
	case ".docx":
		return KindDocument, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", nil
	case ".doc":
		return KindDocument, "application/msword", nil
	case ".pptx":
		return KindDocument, "application/vnd.openxmlformats-officedocument.presentationml.presentation", nil
	case ".ppt":
		return KindDocument, "application/vnd.ms-powerpoint", nil
	case ".rtf":
		return KindDocument, "application/rtf", nil
	}

	textExts := map[string]string{
		".txt": "text/plain", ".md": "text/markdown", ".markdown": "text/markdown",
		".json": "application/json", ".jsonl": "application/json",
		".yaml": "text/yaml", ".yml": "text/yaml", ".toml": "text/plain",
		".xml": "application/xml",
		".log": "text/plain", ".ini": "text/plain", ".conf": "text/plain",
		".sh": "text/x-shellscript", ".bash": "text/x-shellscript", ".zsh": "text/x-shellscript",
		".py": "text/x-python", ".go": "text/x-go", ".ts": "text/typescript", ".tsx": "text/typescript",
		".js": "text/javascript", ".jsx": "text/javascript", ".rs": "text/x-rust",
		".java": "text/x-java", ".c": "text/x-c", ".cpp": "text/x-c++", ".h": "text/x-c",
		".cs": "text/plain", ".rb": "text/plain", ".php": "text/plain", ".swift": "text/plain",
		".kt": "text/plain", ".dart": "text/plain", ".sql": "text/x-sql",
		".html": "text/html", ".css": "text/css", ".scss": "text/css",
		".vue": "text/plain", ".svelte": "text/plain",
	}
	if mapped, ok := textExts[ext]; ok {
		return KindText, mapped, nil
	}
	if strings.HasPrefix(lowerMime, "text/") ||
		lowerMime == "application/json" ||
		lowerMime == "application/xml" ||
		lowerMime == "application/javascript" ||
		lowerMime == "application/typescript" ||
		lowerMime == "application/x-yaml" {
		return KindText, lowerMime, nil
	}

	return "", "", fmt.Errorf("unsupported attachment type %q (%s); supported types: images, PDF, spreadsheets (Excel/CSV/TSV), documents (Word/RTF), and text/code files", filename, mimeType)
}

func SanitizeFilename(name, mimeType string) string {
	base := filepath.Base(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' || r > 127 {
			b.WriteRune(r)
		}
	}
	cleaned := strings.Trim(b.String(), " ._-")
	if cleaned == "" {
		cleaned = "attachment"
	}
	if filepath.Ext(cleaned) == "" {
		switch mimeType {
		case "image/jpeg":
			cleaned += ".jpg"
		case "image/webp":
			cleaned += ".webp"
		case "image/gif":
			cleaned += ".gif"
		case "application/pdf":
			cleaned += ".pdf"
		case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
			cleaned += ".xlsx"
		case "application/vnd.ms-excel":
			cleaned += ".xls"
		case "application/vnd.oasis.opendocument.spreadsheet":
			cleaned += ".ods"
		case "text/csv":
			cleaned += ".csv"
		case "text/tab-separated-values":
			cleaned += ".tsv"
		case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
			cleaned += ".docx"
		case "application/msword":
			cleaned += ".doc"
		case "application/vnd.openxmlformats-officedocument.presentationml.presentation":
			cleaned += ".pptx"
		case "application/vnd.ms-powerpoint":
			cleaned += ".ppt"
		case "application/rtf":
			cleaned += ".rtf"
		case "text/markdown":
			cleaned += ".md"
		default:
			if strings.HasPrefix(mimeType, "text/") {
				cleaned += ".txt"
			} else {
				cleaned += ".png"
			}
		}
	}
	return cleaned
}

func DecodeDataURL(dataURL, fallbackMime string) (string, []byte, error) {
	trimmed := strings.TrimSpace(dataURL)
	if !strings.HasPrefix(trimmed, "data:") {
		return "", nil, errors.New("not a data URL")
	}
	comma := strings.IndexByte(trimmed, ',')
	if comma < 0 {
		return "", nil, errors.New("malformed data URL")
	}
	header := trimmed[5:comma]
	payload := strings.TrimSpace(trimmed[comma+1:])
	mimeType := strings.TrimSpace(strings.Split(header, ";")[0])
	if mimeType == "" {
		mimeType = fallbackMime
	}
	if strings.Contains(header, ";base64") {
		raw, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return "", nil, err
		}
		return mimeType, raw, nil
	}
	return mimeType, []byte(payload), nil
}

func ResolveRefID(rawURL string) string {
	u := strings.TrimSpace(rawURL)
	if idx := strings.IndexByte(u, '?'); idx >= 0 {
		u = u[:idx]
	}
	const prefix = "/api/hoxi/attachments/"
	if idx := strings.Index(u, prefix); idx >= 0 {
		rest := strings.TrimPrefix(u[idx:], prefix)
		parts := strings.Split(strings.Trim(rest, "/"), "/")
		if len(parts) > 0 && isValidAttachmentID(parts[0]) {
			return parts[0]
		}
	}
	return ""
}

func (s *Store) itemDir(id string) string {
	return filepath.Join(s.baseDir, id)
}

func (s *Store) Save(req SaveRequest) (Metadata, error) {
	raw := req.Data
	mimeType := strings.TrimSpace(req.Mime)
	if len(raw) == 0 && strings.TrimSpace(req.DataURL) != "" {
		parsedMime, decoded, err := DecodeDataURL(req.DataURL, mimeType)
		if err != nil {
			return Metadata{}, fmt.Errorf("invalid attachment data: %w", err)
		}
		raw = decoded
		if mimeType == "" {
			mimeType = parsedMime
		}
	}
	if len(raw) == 0 {
		return Metadata{}, errors.New("attachment file is empty")
	}
	if len(raw) > MaxAttachmentBytes {
		return Metadata{}, fmt.Errorf("attachment exceeds maximum size of %d MB", MaxAttachmentBytes/(1024*1024))
	}

	kind, normMime, err := ClassifyFile(req.Filename, mimeType)
	if err != nil {
		return Metadata{}, err
	}
	safeName := SanitizeFilename(req.Filename, normMime)

	// Convert non-standard macOS image formats (BMP/TIFF/HEIC) to PNG on ingest.
	if kind == KindImage && normMime != "image/png" && normMime != "image/jpeg" && normMime != "image/webp" && normMime != "image/gif" {
		if converted, err := convertImageBytesToPNG(raw, filepath.Ext(safeName)); err == nil && len(converted) > 0 {
			raw = converted
			normMime = "image/png"
			safeName = strings.TrimSuffix(safeName, filepath.Ext(safeName)) + ".png"
		}
	}

	now := time.Now().UnixMilli()
	id := newAttachmentID(now)
	dir := s.itemDir(id)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Metadata{}, err
	}
	blobPath := filepath.Join(dir, "blob_"+safeName)
	if err := writeFileAtomic(blobPath, raw); err != nil {
		return Metadata{}, err
	}

	meta := Metadata{
		ID:        id,
		SessionID: strings.TrimSpace(req.SessionID),
		Directory: strings.TrimSpace(req.Directory),
		Filename:  safeName,
		Mime:      normMime,
		Size:      int64(len(raw)),
		Kind:      kind,
		URL:       "/api/hoxi/attachments/" + id + "/content",
		CreatedAt: now,
	}
	if kind == KindText {
		preview := strings.TrimSpace(string(raw))
		if len(preview) > 240 {
			preview = truncateUTF8(preview, 240) + "…"
		}
		meta.TextPreview = preview
	}

	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return Metadata{}, err
	}
	if err := writeFileAtomic(filepath.Join(dir, "meta.json"), append(metaBytes, '\n')); err != nil {
		return Metadata{}, err
	}
	return meta, nil
}

func (s *Store) Get(id string) (Metadata, string, error) {
	if !isValidAttachmentID(id) {
		return Metadata{}, "", os.ErrNotExist
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	dir := s.itemDir(id)
	rawMeta, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return Metadata{}, "", err
	}
	var meta Metadata
	if err := json.Unmarshal(rawMeta, &meta); err != nil {
		return Metadata{}, "", err
	}
	blobPath := filepath.Join(dir, "blob_"+meta.Filename)
	if _, err := os.Stat(blobPath); err != nil {
		return Metadata{}, "", err
	}
	return meta, blobPath, nil
}

func (s *Store) ReadBytes(id string) (Metadata, []byte, error) {
	meta, blobPath, err := s.Get(id)
	if err != nil {
		return Metadata{}, nil, err
	}
	data, err := os.ReadFile(blobPath)
	if err != nil {
		return Metadata{}, nil, err
	}
	return meta, data, nil
}

func (s *Store) ReadAsDataURL(id string) (Metadata, string, error) {
	meta, raw, err := s.ReadBytes(id)
	if err != nil {
		return Metadata{}, "", err
	}
	return meta, "data:" + meta.Mime + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
}

func (s *Store) MaterializeToDir(id, targetDir string) (Metadata, string, error) {
	meta, raw, err := s.ReadBytes(id)
	if err != nil {
		return Metadata{}, "", err
	}
	wt := strings.TrimSpace(targetDir)
	if wt == "" {
		return Metadata{}, "", errors.New("target directory is empty")
	}
	outDir := filepath.Join(wt, ".hoxi", "attachments")
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return Metadata{}, "", err
	}
	dst := filepath.Join(outDir, id+"_"+meta.Filename)
	if err := writeFileAtomic(dst, raw); err != nil {
		return Metadata{}, "", err
	}
	return meta, dst, nil
}

// DeleteBySession removes all stored attachment directories belonging to sessionID.
func (s *Store) DeleteBySession(sessionID string) int {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return 0
	}
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() || !isValidAttachmentID(entry.Name()) {
			continue
		}
		dir := filepath.Join(s.baseDir, entry.Name())
		rawMeta, err := os.ReadFile(filepath.Join(dir, "meta.json"))
		if err != nil {
			continue
		}
		var meta Metadata
		if json.Unmarshal(rawMeta, &meta) != nil {
			continue
		}
		if meta.SessionID == sid {
			if os.RemoveAll(dir) == nil {
				removed++
			}
		}
	}
	return removed
}

func (s *Store) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/hoxi/attachments", s.handleUpload)
	mux.HandleFunc("/api/hoxi/attachments/", s.handleItem)
}

func (s *Store) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req SaveRequest
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(MaxAttachmentBytes + (1 << 20)); err != nil {
			writeJSONError(w, http.StatusBadRequest, "failed to parse multipart upload: "+err.Error())
			return
		}
		file, hdr, err := r.FormFile("file")
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "file field is required")
			return
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, MaxAttachmentBytes+1))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		req = SaveRequest{
			SessionID: r.FormValue("sessionID"),
			Directory: firstNonEmpty(r.FormValue("directory"), r.URL.Query().Get("directory")),
			Filename:  hdr.Filename,
			Mime:      hdr.Header.Get("Content-Type"),
			Data:      data,
		}
	} else {
		body, err := io.ReadAll(io.LimitReader(r.Body, MaxAttachmentBytes*2))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON payload")
			return
		}
		if req.Directory == "" {
			req.Directory = r.URL.Query().Get("directory")
		}
	}

	meta, err := s.Save(req)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

func (s *Store) handleItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/hoxi/attachments/")
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(parts) == 0 || !isValidAttachmentID(parts[0]) {
		writeJSONError(w, http.StatusNotFound, "attachment not found")
		return
	}
	id := parts[0]
	meta, blobPath, err := s.Get(id)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "attachment not found")
		return
	}
	if len(parts) == 1 {
		writeJSON(w, http.StatusOK, meta)
		return
	}
	if parts[1] != "content" {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", meta.Mime)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if isActiveContentMime(meta.Mime) {
		// Attachments share the gateway's origin; never let an uploaded
		// HTML or SVG document run script against Hoxi's own APIs.
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src data:; style-src 'unsafe-inline'")
	}
	http.ServeFile(w, r, blobPath)
}

func isActiveContentMime(mime string) bool {
	mime = strings.ToLower(strings.TrimSpace(mime))
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	switch mime {
	case "text/html", "application/xhtml+xml", "image/svg+xml", "text/xml", "application/xml",
		"text/javascript", "application/javascript":
		return true
	}
	return strings.HasSuffix(mime, "+xml")
}

// truncateUTF8 cuts s to at most n bytes without splitting a character.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".att-*")
	if err != nil {
		return err
	}
	tmpName := f.Name()
	defer os.Remove(tmpName)
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
