package gateway

import (
	"bytes"
	"encoding/base64"
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
