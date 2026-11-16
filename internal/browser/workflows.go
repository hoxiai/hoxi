package browser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

const maxUploadBytes = 50 << 20
const maxDownloadBytes = 512 << 20
const maxTransferBytes = 1 << 30

type UploadRequest struct {
	ID       string `json:"id"`
	Multiple bool   `json:"multiple"`
	tab      string
	node     cdp.BackendNodeID
	frame    cdp.FrameID
}

type PageDialog struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Message string `json:"message"`
	Default string `json:"default,omitempty"`
}

type Download struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Status   string `json:"status"`
	Received int64  `json:"received"`
	Total    int64  `json:"total"`
	Saved    string `json:"saved,omitempty"`
	CanOpen  bool   `json:"canOpen"`
	Error    string `json:"error,omitempty"`
}

type WorkflowState struct {
	Upload    *UploadRequest `json:"upload,omitempty"`
	Dialog    *PageDialog    `json:"dialog,omitempty"`
	Downloads []Download     `json:"downloads"`
	Error     string         `json:"error,omitempty"`
}

type workflowTracker struct {
	mu        sync.Mutex
	uploads   map[string]UploadRequest
	dialogs   map[string]PageDialog
	downloads []Download
	root      string
	tabs      map[string]context.Context
	error     string
	uploaded  int64
}

func requestID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(bytes[:])
}

func (p *workflowTracker) snapshot(tab string) WorkflowState {
	p.mu.Lock()
	defer p.mu.Unlock()
	state := WorkflowState{Downloads: append([]Download{}, p.downloads...), Error: p.error}
	if upload, ok := p.uploads[tab]; ok {
		state.Upload = &upload
	}
	if dialog, ok := p.dialogs[tab]; ok {
		state.Dialog = &dialog
	}
	return state
}

func (p *workflowTracker) receivePage(tab string, raw any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch event := raw.(type) {
	case *page.EventFileChooserOpened:
		if event.BackendNodeID != 0 {
			p.uploads[tab] = UploadRequest{ID: requestID(), Multiple: event.Mode == page.FileChooserOpenedModeSelectMultiple, tab: tab, node: event.BackendNodeID, frame: event.FrameID}
		}
	case *page.EventJavascriptDialogOpening:
		p.dialogs[tab] = PageDialog{ID: requestID(), Type: string(event.Type), Message: clipped(event.Message, 4096), Default: clipped(event.DefaultPrompt, 4096)}
	case *page.EventJavascriptDialogClosed:
		delete(p.dialogs, tab)
	case *page.EventFrameNavigated:
		if event.Frame.ParentID == "" || p.uploads[tab].frame == event.Frame.ID {
			delete(p.uploads, tab)
		}
		if event.Frame.ParentID == "" {
			delete(p.dialogs, tab)
		}
	}
}

func clipped(text string, limit int) string {
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return text
}

func safeFilename(raw string) string {
	name := filepath.Base(strings.ReplaceAll(raw, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || r == ':' {
			return '_'
		}
		return r
	}, clipped(name, 160))
	if name == "" || name == "." || name == ".." {
		return "download"
	}
	return name
}

// Returns the GUID to cancel, if a download exceeds the bounded private cache.
func (p *workflowTracker) receiveDownload(raw any) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch event := raw.(type) {
	case *cdpbrowser.EventDownloadWillBegin:
		if !downloadID.MatchString(event.GUID) {
			return ""
		}
		if len(p.downloads) >= 32 {
			p.error = "download_limit"
			return event.GUID
		}
		p.downloads = append(p.downloads, Download{ID: event.GUID, Filename: safeFilename(event.SuggestedFilename), Status: "inProgress"})
	case *cdpbrowser.EventDownloadProgress:
		for i := range p.downloads {
			d := &p.downloads[i]
			if d.ID != event.GUID {
				continue
			}
			if d.Error != "" {
				return ""
			}
			d.Total, d.Received = int64(event.TotalBytes), int64(event.ReceivedBytes)
			var total int64
			for _, row := range p.downloads {
				total += row.Received
			}
			if d.Total > maxDownloadBytes || d.Received > maxDownloadBytes || total > maxTransferBytes {
				d.Status, d.Error = "canceled", "download_limit"
				return d.ID
			}
			d.Status = string(event.State)
		}
	}
	return ""
}

func (sc *scope) setupWorkflows(ctx context.Context) error {
	root, ledgerID, err := sc.ledger.MkdirTemp("browser-transfers", "hoxi-browser-transfers-")
	if err != nil {
		return err
	}
	sc.transferLedgerID = ledgerID
	sc.workflows.mu.Lock()
	sc.workflows.root = root
	sc.workflows.mu.Unlock()
	chromedp.ListenBrowser(sc.browser, func(event any) {
		if guid := sc.workflows.receiveDownload(event); guid != "" {
			go func() {
				cancelCtx, cancel := context.WithTimeout(sc.browser, 5*time.Second)
				defer cancel()
				_ = chromedp.Run(cancelCtx, chromedp.ActionFunc(func(ctx context.Context) error {
					return cdpbrowser.CancelDownload(guid).Do(cdp.WithExecutor(ctx, chromedp.FromContext(ctx).Browser))
				}))
			}()
		}
	})
	return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		return cdpbrowser.SetDownloadBehavior(cdpbrowser.SetDownloadBehaviorBehaviorAllowAndName).
			WithDownloadPath(root).WithEventsEnabled(true).Do(cdp.WithExecutor(ctx, chromedp.FromContext(sc.browser).Browser))
	}))
}

func (sc *scope) trackWorkflows(tab context.Context, id string) {
	if sc.workflows == nil {
		return
	}
	sc.workflows.mu.Lock()
	sc.workflows.tabs[id] = tab
	sc.workflows.mu.Unlock()
	chromedp.ListenTarget(tab, func(event any) { sc.workflows.receivePage(id, event) })
}

func (sc *scope) workflowAction(ctx context.Context, a Action) error {
	if sc.workflows == nil {
		return errors.New("browser workflows unavailable")
	}
	state := sc.workflows.snapshot(sc.tabID)
	switch a.Action {
	case "upload_cancel":
		if state.Upload == nil || state.Upload.ID != a.RequestID {
			return errors.New("file request expired")
		}
		if err := sc.withTab(ctx, dom.SetFileInputFiles([]string{}).WithBackendNodeID(state.Upload.node)); err != nil {
			return err
		}
		sc.workflows.mu.Lock()
		delete(sc.workflows.uploads, sc.tabID)
		sc.workflows.mu.Unlock()
	case "dialog":
		if state.Dialog == nil || state.Dialog.ID != a.RequestID {
			return errors.New("page dialog expired")
		}
		if err := sc.withTab(ctx, page.HandleJavaScriptDialog(*a.Accept).WithPromptText(a.Text)); err != nil {
			return err
		}
	}
	return nil
}
