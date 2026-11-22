package gateway

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/hoxiai/hoxi/internal/attachment"
)

const (
	maxStageFileBytes = 2 << 30 // 2 GiB streaming ceiling for local media/large assets
	stageInputsSubdir = "inputs"
)

// handleFileStage streams a desktop-selected or dropped local file directly into
// <workspace>/inputs/<sanitized-filename> so large media assets (video, audio,
// archives) can be referenced via @inputs/<filename> without base64-inlining.
func (g *Gateway) handleFileStage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	if isRemoteRequest(r) {
		writeJSONError(w, http.StatusForbidden, "staging local files into workspace is desktop-only")
		return
	}

	q := r.URL.Query()
	rawDir := strings.TrimSpace(q.Get("directory"))
	if rawDir == "" {
		rawDir = g.rootDir
	}
	allowedRoots := g.allowedWorkspaceRoots()
	cDir, _, ok := g.isRegisteredWorkspaceOrSandbox(rawDir, allowedRoots)
	if !ok {
		writeJSONError(w, http.StatusForbidden, "directory is not a registered workspace")
		return
	}
	if isSensitiveCredentialPath(cDir) {
		writeJSONError(w, http.StatusForbidden, "staging into a sensitive directory is forbidden")
		return
	}

	rawName := strings.TrimSpace(q.Get("filename"))
	if rawName == "" {
		rawName = strings.TrimSpace(r.Header.Get("X-Hoxi-Filename"))
	}
	safeName, err := sanitizeStageFilename(rawName)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	var expectedSize int64
	hasExpectedSize := false
	if rawExpected := strings.TrimSpace(q.Get("expectedSize")); rawExpected != "" {
		parsed, parseErr := strconv.ParseInt(rawExpected, 10, 64)
		if parseErr != nil || parsed < 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid expectedSize parameter")
			return
		}
		if parsed > maxStageFileBytes {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "file exceeds 2 GiB staging limit")
			return
		}
		expectedSize = parsed
		hasExpectedSize = true
	}

	inputsDir := filepath.Join(cDir, stageInputsSubdir)
	if err := os.MkdirAll(inputsDir, 0o755); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to prepare workspace inputs directory")
		return
	}
	if !isPathWithinDir(cDir, inputsDir) {
		writeJSONError(w, http.StatusForbidden, "inputs directory escapes workspace")
		return
	}

	destPath, finalName, err := allocateUniqueStagePath(inputsDir, safeName)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !isPathWithinDir(inputsDir, destPath) {
		writeJSONError(w, http.StatusForbidden, "target file escapes workspace inputs directory")
		return
	}

	tmpFile, err := os.CreateTemp(inputsDir, ".stage-*.tmp")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to create staging file")
		return
	}
	tmpPath := tmpFile.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	r.Body = http.MaxBytesReader(w, r.Body, maxStageFileBytes)
	written, copyErr := io.Copy(tmpFile, r.Body)
	closeErr := tmpFile.Close()
	if copyErr != nil {
		var maxErr *http.MaxBytesError
		if errors.As(copyErr, &maxErr) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "file exceeds 2 GiB staging limit")
			return
		}
		writeJSONError(w, http.StatusBadRequest, "failed to receive file stream: "+copyErr.Error())
		return
	}
	if closeErr != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to finalize staged file")
		return
	}
	if hasExpectedSize && written != expectedSize {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("incomplete file upload: received %d of %d bytes", written, expectedSize))
		return
	}
	if !hasExpectedSize && r.ContentLength > 0 && written != r.ContentLength {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("incomplete file upload: received %d of %d bytes", written, r.ContentLength))
		return
	}
	if !hasExpectedSize && written == 0 {
		writeJSONError(w, http.StatusBadRequest, "empty file upload body")
		return
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to commit staged file")
		return
	}
	cleanupTmp = false

	relPath := stageInputsSubdir + "/" + finalName
	mimeType := attachment.DetectFileMimeType(destPath, nil)
	mediaURL := "/api/hoxi/file/content?path=" + url.QueryEscape(relPath) + "&directory=" + url.QueryEscape(cDir) + "&raw=1"
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"path":     relPath,
		"absolute": destPath,
		"filename": finalName,
		"mime":     mimeType,
		"url":      mediaURL,
		"size":     written,
	})
}

// sanitizeStageFilename extracts the basename, strips unsafe path/control runes,
// and converts whitespace to underscores so @inputs/<filename> stays a single token.
func sanitizeStageFilename(raw string) (string, error) {
	cleaned := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	if cleaned == "" {
		return "", errors.New("filename is required")
	}
	base := filepath.Base(cleaned)
	var b strings.Builder
	for _, r := range base {
		switch {
		case r < 0x20 || r == 0x7f || r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
			continue
		case unicode.IsSpace(r):
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), ". ")
	if out == "" || out == "." || out == ".." {
		return "", errors.New("invalid filename")
	}
	if len(out) > 180 {
		ext := filepath.Ext(out)
		stem := strings.TrimSuffix(out, ext)
		if len(ext) > 24 {
			ext = ""
		}
		maxStem := 180 - len(ext)
		if maxStem < 1 {
			maxStem = 1
		}
		runes := []rune(stem)
		if len(runes) > maxStem {
			stem = string(runes[:maxStem])
		}
		out = stem + ext
	}
	return out, nil
}

func allocateUniqueStagePath(dir, name string) (string, string, error) {
	cand := filepath.Join(dir, name)
	if _, err := os.Lstat(cand); errors.Is(err, os.ErrNotExist) {
		return cand, name, nil
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 2; i <= 9999; i++ {
		nextName := fmt.Sprintf("%s_%d%s", stem, i, ext)
		nextPath := filepath.Join(dir, nextName)
		if _, err := os.Lstat(nextPath); errors.Is(err, os.ErrNotExist) {
			return nextPath, nextName, nil
		}
	}
	return "", "", errors.New("too many files with the same name in inputs/")
}
