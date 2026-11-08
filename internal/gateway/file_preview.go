package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/hoxiai/hoxi/internal/attachment"
)

func detectFileMimeType(targetPath string, data []byte) string {
	ext := strings.ToLower(filepath.Ext(targetPath))
	switch ext {
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	case ".bmp":
		return "image/bmp"
	case ".avif":
		return "image/avif"
	case ".md", ".mdx", ".markdown":
		return "text/markdown"
	case ".pdf":
		return "application/pdf"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".xls":
		return "application/vnd.ms-excel"
	case ".ods":
		return "application/vnd.oasis.opendocument.spreadsheet"
	case ".csv":
		return "text/csv"
	case ".tsv":
		return "text/tab-separated-values"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".doc":
		return "application/msword"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case ".ppt":
		return "application/vnd.ms-powerpoint"
	case ".rtf":
		return "application/rtf"
	}
	if len(data) > 0 {
		return http.DetectContentType(data)
	}
	return "application/octet-stream"
}

func isBinaryImageMime(mimeType string) bool {
	m := strings.ToLower(strings.TrimSpace(mimeType))
	return strings.HasPrefix(m, "image/") && !strings.HasPrefix(m, "image/svg")
}

func isSpreadsheetExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".xlsx", ".xls", ".ods", ".csv", ".tsv":
		return true
	default:
		return false
	}
}

func isExtractableDocExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".pdf", ".docx", ".doc", ".pptx", ".ppt", ".rtf":
		return true
	default:
		return false
	}
}

func (g *Gateway) handleFileContent(w http.ResponseWriter, r *http.Request) {
	remote := isRemoteRequest(r)
	rawPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if r.Method != http.MethodGet || rawPath == "" {
		// Remote requests never fall through to the OpenCode proxy.
		if remote {
			writeJSONError(w, http.StatusForbidden, "route is not available for remote controllers")
			return
		}
		g.proxy.ServeHTTP(w, r)
		return
	}
	rawDir := strings.TrimSpace(r.URL.Query().Get("directory"))
	sessionID := strings.TrimSpace(r.URL.Query().Get("sessionID"))

	cleanedPreview, _ := cleanRequestedFilePath(rawPath)
	// Remote controllers keep the hard 403 and are never offered a dialog.
	if remote && cleanedPreview != "" && isSensitiveCredentialPath(cleanedPreview) {
		writeJSONError(w, http.StatusForbidden, "access to sensitive credential file is forbidden")
		return
	}

	targetPath, info, outsideWorkspace, err := g.resolveLocalFileTarget(rawPath, rawDir, sessionID, true)
	sensitiveTarget := cleanedPreview != "" && isSensitiveCredentialPath(cleanedPreview)
	if targetPath != "" && isSensitiveCredentialPath(targetPath) {
		sensitiveTarget = true
	}
	if sensitiveTarget {
		confirmPath := targetPath
		if confirmPath == "" {
			confirmPath = cleanedPreview
		}
		if !g.confirmSensitiveFileAccess(r, confirmPath, "read", fileAccessLocale(r)) {
			writeJSONError(w, http.StatusForbidden, "access to sensitive credential file is forbidden")
			return
		}
	}
	// outsideWorkspace is judged against request-supplied directories too, so
	// remote reads must also land inside a registered workspace or worktree.
	// Checked before existence so the tunnel cannot probe arbitrary paths.
	if remote && (outsideWorkspace || !g.tunnelFilePathAllowed(targetPath, sessionID)) {
		writeJSONError(w, http.StatusForbidden, "path is outside the workspace")
		return
	}
	if err != nil && os.IsNotExist(err) && !outsideWorkspace {
		if matPath, matInfo, ok := g.tryMaterializePreviewAttachment(targetPath); ok {
			targetPath = matPath
			info = matInfo
			err = nil
		}
	}
	if err != nil {
		if os.IsNotExist(err) {
			writeJSONError(w, http.StatusNotFound, "file not found: "+rawPath)
			return
		}
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if info.IsDir() {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":            "path is a directory: " + rawPath,
			"isDirectory":      true,
			"resolvedPath":     targetPath,
			"outsideWorkspace": outsideWorkspace,
		})
		return
	}

	f, err := os.Open(targetPath)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer f.Close()
	if openedInfo, statErr := f.Stat(); statErr != nil || openedInfo.IsDir() || !os.SameFile(info, openedInfo) {
		writeJSONError(w, http.StatusBadRequest, "file target changed during open")
		return
	} else {
		info = openedInfo
	}

	data, err := io.ReadAll(io.LimitReader(f, maxLocalFilePreviewBytes))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	ext := strings.ToLower(filepath.Ext(targetPath))
	mimeType := detectFileMimeType(targetPath, data)

	var sheets []attachment.SpreadsheetSheet
	var extractedText string
	if isSpreadsheetExt(ext) {
		if parsed, sErr := attachment.ParseSpreadsheetFile(targetPath, 500); sErr == nil && len(parsed) > 0 {
			sheets = parsed
			if ext != ".csv" && ext != ".tsv" {
				extractedText = attachment.FormatSpreadsheetSheets(parsed)
			}
		}
	} else if isExtractableDocExt(ext) {
		extractedText = attachment.ExtractFileText(targetPath, 64000)
	}

	if isBinaryImageMime(mimeType) || bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		resp := map[string]any{
			"type":             "binary",
			"content":          base64.StdEncoding.EncodeToString(data),
			"encoding":         "base64",
			"mimeType":         mimeType,
			"resolvedPath":     targetPath,
			"outsideWorkspace": outsideWorkspace,
			"size":             info.Size(),
		}
		if len(sheets) > 0 {
			resp["sheets"] = sheets
		}
		if extractedText != "" {
			resp["extractedText"] = extractedText
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	resp := map[string]any{
		"type":             "text",
		"content":          string(data),
		"mimeType":         mimeType,
		"resolvedPath":     targetPath,
		"outsideWorkspace": outsideWorkspace,
		"size":             info.Size(),
	}
	if len(sheets) > 0 {
		resp["sheets"] = sheets
	}
	if extractedText != "" {
		resp["extractedText"] = extractedText
	}
	writeJSON(w, http.StatusOK, resp)
}

func (g *Gateway) tryMaterializePreviewAttachment(targetPath string) (string, os.FileInfo, bool) {
	if g == nil || g.runner == nil || targetPath == "" {
		return "", nil, false
	}
	attDir := filepath.Dir(targetPath)
	dotHoxiDir := filepath.Dir(attDir)
	if filepath.Base(attDir) != "attachments" || filepath.Base(dotHoxiDir) != ".hoxi" {
		return "", nil, false
	}
	base := filepath.Base(targetPath)
	if !strings.HasPrefix(base, "att_") {
		return "", nil, false
	}
	attID := base
	if idx := strings.IndexByte(base[4:], '_'); idx > 0 {
		attID = base[:4+idx]
	}
	worktreeRoot := filepath.Dir(dotHoxiDir)
	dst, err := g.runner.MaterializeAttachmentToDir(attID, worktreeRoot)
	if err != nil || dst == "" {
		return "", nil, false
	}
	info, err := os.Stat(dst)
	if err != nil || info.IsDir() {
		return "", nil, false
	}
	return dst, info, true
}

func isProtectedSpreadsheetWriteTarget(targetPath string) bool {
	slash := strings.ToLower(filepath.ToSlash(filepath.Clean(targetPath)))
	if idx := strings.LastIndex(slash, "/.hoxi/worktrees/"); idx >= 0 {
		rest := slash[idx+len("/.hoxi/worktrees/"):]
		if slashIdx := strings.IndexByte(rest, '/'); slashIdx >= 0 {
			slash = rest[slashIdx+1:]
		}
	}
	if strings.Contains(slash, "/.hoxi/attachments/") || strings.HasPrefix(slash, ".hoxi/attachments/") {
		return false
	}
	for _, seg := range strings.Split(slash, "/") {
		switch seg {
		case ".git", ".opencode", ".claude", ".hoxi", ".agents":
			return true
		}
	}
	return false
}

func (g *Gateway) handleFileSpreadsheetEdit(w http.ResponseWriter, r *http.Request) {
	if isRemoteRequest(r) {
		writeJSONError(w, http.StatusForbidden, "route is not available for remote controllers")
		return
	}
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var body struct {
		Path      string            `json:"path"`
		Directory string            `json:"directory"`
		SessionID string            `json:"sessionID"`
		Sheet     string            `json:"sheet"`
		Edits     map[string]string `json:"edits"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	rawPath := strings.TrimSpace(body.Path)
	if rawPath == "" || len(body.Edits) == 0 {
		writeJSONError(w, http.StatusBadRequest, "path and edits are required")
		return
	}

	targetPath, info, outsideWorkspace, err := g.resolveLocalFileTarget(rawPath, body.Directory, body.SessionID, false)
	if err != nil && os.IsNotExist(err) && !outsideWorkspace {
		if matPath, matInfo, ok := g.tryMaterializePreviewAttachment(targetPath); ok {
			targetPath = matPath
			info = matInfo
			err = nil
		}
	}
	if outsideWorkspace {
		writeJSONError(w, http.StatusForbidden, "cannot edit spreadsheet outside the workspace")
		return
	}
	if isSensitiveCredentialPath(targetPath) || isProtectedSpreadsheetWriteTarget(targetPath) {
		writeJSONError(w, http.StatusForbidden, "access to protected or credential file is forbidden")
		return
	}
	if err != nil || info == nil || info.IsDir() {
		writeJSONError(w, http.StatusNotFound, "file not found: "+rawPath)
		return
	}

	ext := strings.ToLower(filepath.Ext(targetPath))
	if ext != ".xlsx" && ext != ".csv" && ext != ".tsv" {
		writeJSONError(w, http.StatusBadRequest, "inline editing is supported for .xlsx, .csv, and .tsv files")
		return
	}

	if _, err := attachment.ApplySpreadsheetEdits(targetPath, targetPath, body.Sheet, body.Edits, nil); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	sheets, _ := attachment.ParseSpreadsheetFile(targetPath, 500)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"resolvedPath": targetPath,
		"sheets":       sheets,
	})
}
