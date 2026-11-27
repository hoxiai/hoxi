package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/attachment"
)

const (
	maxSaveImagesCount        = 32
	maxSaveImageBytes         = 20 << 20
	maxSaveImagesTotalBytes   = 80 << 20
	maxSaveImagesRequestBytes = int64(maxSaveImagesTotalBytes)*4/3 + (1 << 20)
)

var pngFileSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// SetSaveFileDialog installs the native desktop save-file picker callback used
// when exporting generated assets (such as share PNG cards) from the desktop UI.
func (g *Gateway) SetSaveFileDialog(fn func(SaveFileOptions) (string, error)) {
	g.dialogMu.Lock()
	g.onSaveFileDialog = fn
	g.dialogMu.Unlock()
}

type saveImageInput struct {
	Filename string `json:"filename"`
	DataURL  string `json:"dataUrl"`
}

type decodedSaveImage struct {
	filename string
	data     []byte
}

func sanitizeSavePNGFilename(raw string, fallbackIndex int) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	base := filepath.Base(normalized)
	if base == "." || base == "/" {
		base = ""
	}
	var b strings.Builder
	for _, r := range base {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' {
			b.WriteByte('-')
			continue
		}
		b.WriteRune(r)
	}
	cleaned := strings.TrimSpace(b.String())
	cleaned = strings.TrimLeft(cleaned, ".")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		if fallbackIndex > 0 {
			return fmt.Sprintf("hoxi-share-%d.png", fallbackIndex)
		}
		return "hoxi-share.png"
	}
	stem := cleaned
	if strings.EqualFold(filepath.Ext(cleaned), ".png") {
		stem = strings.TrimSpace(cleaned[:len(cleaned)-4])
	}
	runes := []rune(stem)
	if len(runes) > 80 {
		stem = strings.TrimSpace(string(runes[:80]))
	}
	if stem == "" || stem == "." || stem == ".." {
		if fallbackIndex > 0 {
			return fmt.Sprintf("hoxi-share-%d.png", fallbackIndex)
		}
		return "hoxi-share.png"
	}
	return stem + ".png"
}

func defaultUserDownloadsDir() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	downloads := filepath.Join(home, "Downloads")
	if info, statErr := os.Stat(downloads); statErr == nil && info.IsDir() {
		return downloads
	}
	if info, statErr := os.Stat(home); statErr == nil && info.IsDir() {
		return home
	}
	return ""
}

func isProtectedSaveDestination(destPath string) bool {
	clean, ok := sanitizeDesktopDirectory(destPath)
	if !ok || clean == "" {
		return true
	}
	dir := filepath.Dir(clean)
	if isSensitiveCredentialPath(clean) || isSensitiveCredentialPath(dir) || isProtectedPickedDirectory(dir) {
		return true
	}
	resolved, resolvedOK := resolveGatewayPathWithSymlinks(clean, 0)
	if !resolvedOK || resolved == "" {
		return true
	}
	resolvedDir := filepath.Dir(resolved)
	return isSensitiveCredentialPath(resolved) || isSensitiveCredentialPath(resolvedDir) || isProtectedPickedDirectory(resolvedDir)
}

func validateExistingSaveTarget(destPath string) error {
	info, err := os.Lstat(destPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.IsDir() {
		return errors.New("destination path is a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refusing to overwrite symlink destination")
	}
	if !info.Mode().IsRegular() {
		return errors.New("destination path is not a regular file")
	}
	return nil
}

func writePNGFileAtomic(destPath string, data []byte) error {
	dir := filepath.Dir(destPath)
	tmp, err := os.CreateTemp(dir, ".hoxi-share-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return err
	}
	committed = true
	return nil
}

func pathExistsLstat(p string) bool {
	_, err := os.Lstat(p)
	return err == nil || !os.IsNotExist(err)
}

func allocateBatchPNGPaths(dir string, filenames []string) []string {
	// Ensure names within the batch are distinct before checking disk collisions.
	baseNames := make([]string, len(filenames))
	seenInBatch := make(map[string]bool, len(filenames))
	for i, name := range filenames {
		candidate := name
		if seenInBatch[strings.ToLower(candidate)] {
			ext := filepath.Ext(name)
			stem := strings.TrimSuffix(name, ext)
			for suffix := 2; ; suffix++ {
				candidate = fmt.Sprintf("%s-%d%s", stem, suffix, ext)
				if !seenInBatch[strings.ToLower(candidate)] {
					break
				}
			}
		}
		seenInBatch[strings.ToLower(candidate)] = true
		baseNames[i] = candidate
	}

	buildAttempt := func(suffix string) []string {
		out := make([]string, len(baseNames))
		for i, name := range baseNames {
			if suffix == "" {
				out[i] = filepath.Join(dir, name)
				continue
			}
			ext := filepath.Ext(name)
			stem := strings.TrimSuffix(name, ext)
			out[i] = filepath.Join(dir, fmt.Sprintf("%s-%s%s", stem, suffix, ext))
		}
		return out
	}

	hasCollision := func(paths []string) bool {
		for _, p := range paths {
			if pathExistsLstat(p) {
				return true
			}
		}
		return false
	}

	if first := buildAttempt(""); !hasCollision(first) {
		return first
	}
	for attempt := 2; attempt <= 99; attempt++ {
		if cand := buildAttempt(fmt.Sprintf("copy%d", attempt)); !hasCollision(cand) {
			return cand
		}
	}
	return buildAttempt(fmt.Sprintf("%d", time.Now().UnixMilli()))
}

func (g *Gateway) handleSaveImages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	if err := g.verifyStrictDesktopTokenRequest("Save images")(r); err != nil {
		writeJSONError(w, http.StatusForbidden, err.Error())
		return
	}

	var body struct {
		Title      string           `json:"title"`
		DefaultDir string           `json:"defaultDir"`
		Images     []saveImageInput `json:"images"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxSaveImagesRequestBytes)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.Images) == 0 || len(body.Images) > maxSaveImagesCount {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("images must contain 1..%d PNG items", maxSaveImagesCount))
		return
	}

	decoded := make([]decodedSaveImage, 0, len(body.Images))
	var totalBytes int64
	for i, item := range body.Images {
		mimeType, raw, err := attachment.DecodeDataURL(item.DataURL, "")
		if err != nil || len(raw) == 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid image dataUrl")
			return
		}
		if len(raw) > maxSaveImageBytes {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "image exceeds size limit")
			return
		}
		totalBytes += int64(len(raw))
		if totalBytes > maxSaveImagesTotalBytes {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "total images exceed size limit")
			return
		}
		if !strings.EqualFold(strings.TrimSpace(mimeType), "image/png") ||
			!bytes.HasPrefix(raw, pngFileSignature) ||
			http.DetectContentType(raw) != "image/png" {
			writeJSONError(w, http.StatusBadRequest, "only PNG image payloads are supported")
			return
		}
		fallbackIdx := 0
		if len(body.Images) > 1 {
			fallbackIdx = i + 1
		}
		decoded = append(decoded, decodedSaveImage{
			filename: sanitizeSavePNGFilename(item.Filename, fallbackIdx),
			data:     raw,
		})
	}

	g.dialogMu.RLock()
	saveDialog := g.onSaveFileDialog
	dirDialog := g.onOpenDirectoryDialog
	g.dialogMu.RUnlock()

	if len(decoded) == 1 && saveDialog == nil {
		writeJSON(w, http.StatusOK, map[string]any{"supported": false})
		return
	}
	if len(decoded) > 1 && dirDialog == nil {
		writeJSON(w, http.StatusOK, map[string]any{"supported": false})
		return
	}

	if !g.dialogPicking.CompareAndSwap(false, true) {
		writeJSONError(w, http.StatusConflict, "file dialog is already open")
		return
	}
	defer g.dialogPicking.Store(false)

	defaultDir := sanitizeExistingDialogDir(body.DefaultDir)
	if defaultDir == "" || isProtectedPickedDirectory(defaultDir) || isSensitiveCredentialPath(defaultDir) {
		defaultDir = defaultUserDownloadsDir()
	}
	title := sanitizeDialogTitle(body.Title)

	if len(decoded) == 1 {
		chosen, err := saveDialog(SaveFileOptions{
			Title:            title,
			DefaultFilename:  decoded[0].filename,
			DefaultDirectory: defaultDir,
			FilterName:       "PNG Image (*.png)",
			FilterPattern:    "*.png",
		})
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		dest := strings.TrimSpace(chosen)
		if dest == "" {
			writeJSON(w, http.StatusOK, map[string]any{
				"supported": true,
				"saved":     false,
				"canceled":  true,
				"paths":     []string{},
			})
			return
		}
		if !filepath.IsAbs(dest) {
			writeJSONError(w, http.StatusBadRequest, "save path must be absolute")
			return
		}
		dest = filepath.Clean(dest)
		if !strings.EqualFold(filepath.Ext(dest), ".png") {
			dest += ".png"
		}
		if isProtectedSaveDestination(dest) {
			writeJSONError(w, http.StatusForbidden, "selected destination is protected")
			return
		}
		if err := validateExistingSaveTarget(dest); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := writePNGFileAtomic(dest, decoded[0].data); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to save image: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"supported": true,
			"saved":     true,
			"canceled":  false,
			"paths":     []string{dest},
			"directory": filepath.Dir(dest),
		})
		return
	}

	chosenDir, err := dirDialog(OpenDirectoryOptions{
		Title:            title,
		DefaultDirectory: defaultDir,
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dir := strings.TrimSpace(chosenDir)
	if dir == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"supported": true,
			"saved":     false,
			"canceled":  true,
			"paths":     []string{},
		})
		return
	}
	dir = sanitizeExistingDialogDir(dir)
	if dir == "" {
		writeJSONError(w, http.StatusBadRequest, "selected directory is unavailable")
		return
	}
	if isProtectedPickedDirectory(dir) || isSensitiveCredentialPath(dir) {
		writeJSONError(w, http.StatusForbidden, "selected directory is protected")
		return
	}

	names := make([]string, len(decoded))
	for i, item := range decoded {
		names[i] = item.filename
	}
	targets := allocateBatchPNGPaths(dir, names)
	savedPaths := make([]string, 0, len(targets))
	for i, target := range targets {
		if !isPathWithinDir(dir, target) || isProtectedSaveDestination(target) {
			writeJSONError(w, http.StatusForbidden, "destination path is protected")
			return
		}
		if err := validateExistingSaveTarget(target); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := writePNGFileAtomic(target, decoded[i].data); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to save image: "+err.Error())
			return
		}
		savedPaths = append(savedPaths, target)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"supported": true,
		"saved":     true,
		"canceled":  false,
		"paths":     savedPaths,
		"directory": dir,
	})
}
