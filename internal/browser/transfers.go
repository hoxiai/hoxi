package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/chromedp"
)

var downloadID = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

// Callbacks are supplied by the desktop host, never by HTTP request fields.
func (s *Service) SetFileUI(save func(name, title string) (string, error), open func(path string, reveal bool) error) {
	s.mu.Lock()
	s.saveFileDialog, s.openFile = save, open
	s.mu.Unlock()
}

func (s *Service) serveTransfer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	agent, err := s.authenticate(r)
	if err != nil || agent {
		fail(w, 403, errors.New("only UI may transfer browser files"))
		return
	}
	if r.Method != http.MethodPost {
		fail(w, 405, errors.New("POST required"))
		return
	}
	key, err := s.requestScope(r)
	if err != nil {
		fail(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	sc, err := s.acquire(ctx, key, false)
	if err != nil || sc == nil {
		fail(w, 409, errors.New("browser not running"))
		return
	}
	locked := true
	defer func() {
		if locked {
			sc.lastUsed = s.now()
			<-sc.gate
		}
	}()
	if sc.extension != nil || sc.workflows == nil {
		fail(w, 409, errors.New("use Chrome's native upload and download dialogs for Chrome Link"))
		return
	}
	if strings.HasSuffix(r.URL.Path, "/upload") {
		if !sc.manual {
			fail(w, 409, errors.New("take manual control before uploading"))
			return
		}
		err = sc.uploadFiles(ctx, w, r)
		if err != nil {
			fail(w, 400, err)
			return
		}
		writeJSON(w, 200, sc.workflows.snapshot(sc.tabID))
		return
	}
	var req struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil || !downloadID.MatchString(req.ID) || len(req.Title) > 256 {
		fail(w, 400, errors.New("one valid download id is required"))
		return
	}
	var download *Download
	for _, item := range sc.workflows.snapshot(sc.tabID).Downloads {
		if item.ID == req.ID {
			copy := item
			download = &copy
			break
		}
	}
	if download == nil {
		fail(w, 404, errors.New("download not found in this browser"))
		return
	}
	if strings.HasSuffix(r.URL.Path, "/download-cancel") {
		err = chromedp.Run(sc.browser, chromedp.ActionFunc(func(ctx context.Context) error {
			return cdpbrowser.CancelDownload(req.ID).Do(cdp.WithExecutor(ctx, chromedp.FromContext(sc.browser).Browser))
		}))
	} else if download.Status != "completed" {
		err = errors.New("download is not complete")
	} else if strings.HasSuffix(r.URL.Path, "/download-save") {
		s.mu.Lock()
		dialog := s.saveFileDialog
		s.mu.Unlock()
		if dialog == nil {
			fail(w, 503, errors.New("native save dialog requires the desktop app"))
			return
		}
		// Do not hold the page gate while the user chooses a save location.
		<-sc.gate
		locked = false
		path, dialogErr := dialog(download.Filename, req.Title)
		if dialogErr != nil {
			err = dialogErr
		} else if path == "" {
			writeJSON(w, 200, map[string]bool{"canceled": true})
			return
		} else {
			next, acquireErr := s.acquire(ctx, key, false)
			if acquireErr != nil || next != sc {
				if next != nil {
					<-next.gate
				}
				err = errors.New("browser closed while choosing a save location")
			} else {
				locked = true
				err = saveDownloadedFile(ctx, filepath.Join(sc.workflows.root, req.ID), path)
				if err == nil {
					sc.workflows.mu.Lock()
					for i := range sc.workflows.downloads {
						if sc.workflows.downloads[i].ID == req.ID {
							sc.workflows.downloads[i].Saved = path
							sc.workflows.downloads[i].CanOpen = safeDocument(path)
						}
					}
					sc.workflows.mu.Unlock()
				}
			}
		}
	} else {
		if download.Saved == "" {
			err = errors.New("save the download first")
		} else if info, e := os.Lstat(download.Saved); e != nil || !info.Mode().IsRegular() {
			err = errors.New("saved file is missing or was replaced")
		} else {
			reveal := strings.HasSuffix(r.URL.Path, "/download-reveal")
			s.mu.Lock()
			open := s.openFile
			s.mu.Unlock()
			if !reveal && !safeDocument(download.Saved) {
				err = errors.New("reveal executable downloads in Finder to open them explicitly")
			} else if open == nil {
				err = errors.New("desktop file opener unavailable")
			} else {
				err = open(download.Saved, reveal)
			}
		}
	}
	if err != nil {
		fail(w, 409, err)
		return
	}
	writeJSON(w, 200, sc.workflows.snapshot(sc.tabID))
}

func (sc *scope) uploadFiles(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		return errors.New("upload exceeds 50 MiB or has invalid form data")
	}
	defer r.MultipartForm.RemoveAll()
	if len(r.MultipartForm.Value) != 1 || len(r.MultipartForm.Value["id"]) != 1 || len(r.MultipartForm.File) != 1 {
		return errors.New("only id and files fields are accepted")
	}
	upload := sc.workflows.snapshot(sc.tabID).Upload
	if upload == nil || upload.ID != r.FormValue("id") {
		return errors.New("file request expired")
	}
	files := r.MultipartForm.File["files"]
	if len(files) < 1 || len(files) > 16 || (!upload.Multiple && len(files) != 1) {
		return errors.New("choose 1..16 files matching the page request")
	}
	var size int64
	for _, file := range files {
		size += file.Size
	}
	if size > maxUploadBytes || sc.workflows.uploaded+size > 128<<20 {
		return errors.New("upload storage limit exceeded (50 MiB per selection, 128 MiB per browser)")
	}
	stage, err := os.MkdirTemp(sc.workflows.root, "upload-")
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(stage)
		}
	}()
	paths := []string{}
	for i, file := range files {
		folder := filepath.Join(stage, fmt.Sprint(i))
		if err = os.Mkdir(folder, 0700); err != nil {
			return err
		}
		path := filepath.Join(folder, safeFilename(file.Filename))
		source, e := file.Open()
		if e != nil {
			return e
		}
		target, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			source.Close()
			return e
		}
		_, e = io.Copy(target, io.LimitReader(source, maxUploadBytes+1))
		source.Close()
		closeErr := target.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		paths = append(paths, path)
	}
	if err = sc.withTab(ctx, dom.SetFileInputFiles(paths).WithBackendNodeID(upload.node)); err != nil {
		return err
	}
	sc.workflows.mu.Lock()
	delete(sc.workflows.uploads, sc.tabID)
	sc.workflows.uploaded += size
	sc.workflows.mu.Unlock()
	committed = true
	return nil
}

func safeDocument(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	mimeType := mime.TypeByExtension(ext)
	return strings.HasPrefix(mimeType, "image/") && ext != ".svg" || strings.HasPrefix(mimeType, "text/") && (ext == ".txt" || ext == ".csv" || ext == ".md") ||
		ext == ".pdf" || ext == ".docx" || ext == ".xlsx" || ext == ".pptx"
}

func saveDownloadedFile(ctx context.Context, source, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsAbs(destination) {
		return errors.New("save path must be absolute")
	}
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxDownloadBytes {
		return errors.New("download file is unavailable")
	}
	if target, e := os.Lstat(destination); e == nil && !target.Mode().IsRegular() {
		return errors.New("save target is not a regular file")
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(destination), ".hoxi-download-*")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	_, copyErr := io.Copy(out, io.LimitReader(in, maxDownloadBytes+1))
	if copyErr == nil {
		copyErr = out.Sync()
	}
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := quarantineDownload(out.Name()); err != nil {
		return err
	}
	return os.Rename(out.Name(), destination)
}
