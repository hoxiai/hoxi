package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"

	"github.com/hoxiai/hoxi/internal/attachment"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/intake"
	"github.com/hoxiai/hoxi/internal/runner"
	"github.com/hoxiai/hoxi/internal/worktree"
)

type gatewayIntakeAttachments struct {
	store *attachment.Store
}

func (a gatewayIntakeAttachments) MaxBytes() int {
	return attachment.MaxAttachmentBytes
}

func (a gatewayIntakeAttachments) Save(directory, filename, mime string, data []byte) (intake.AttachmentRef, error) {
	meta, err := a.store.Save(attachment.SaveRequest{
		Directory: directory,
		Filename:  filename,
		Mime:      mime,
		Data:      data,
	})
	if err != nil {
		return intake.AttachmentRef{}, err
	}
	return intake.AttachmentRef{
		ID:       meta.ID,
		Filename: meta.Filename,
		Mime:     meta.Mime,
		Size:     meta.Size,
		Kind:     string(meta.Kind),
	}, nil
}

func (a gatewayIntakeAttachments) Get(id string) (intake.AttachmentRef, error) {
	meta, _, err := a.store.Get(id)
	if err != nil {
		return intake.AttachmentRef{}, err
	}
	return intake.AttachmentRef{
		ID:       meta.ID,
		Filename: meta.Filename,
		Mime:     meta.Mime,
		Size:     meta.Size,
		Kind:     string(meta.Kind),
	}, nil
}

func (a gatewayIntakeAttachments) ReadBytes(id string) (intake.AttachmentRef, []byte, error) {
	meta, raw, err := a.store.ReadBytes(id)
	if err != nil {
		return intake.AttachmentRef{}, nil, err
	}
	return intake.AttachmentRef{
		ID:       meta.ID,
		Filename: meta.Filename,
		Mime:     meta.Mime,
		Size:     meta.Size,
		Kind:     string(meta.Kind),
	}, raw, nil
}

func (a gatewayIntakeAttachments) Delete(id string) error {
	if a.store == nil {
		return nil
	}
	return a.store.Delete(id)
}

func newGatewayIntakeService(g *Gateway) *intake.Service {
	var att intake.Attachments
	var bridge *gatewayIntakeSessions
	var sess intake.Sessions
	if g.runner != nil {
		if st := g.runner.Attachments(); st != nil {
			att = gatewayIntakeAttachments{store: st}
		}
		bridge = &gatewayIntakeSessions{runner: g.runner}
		sess = bridge
	}
	svc := intake.NewService(hoxipaths.IntakeStorePath(), att, sess, g.resolveIntakeEndpoints)
	if bridge != nil {
		svc.SetSessionWatcher(func() func() {
			return bridge.startSessionWatcher(svc)
		})
	}
	return svc
}

func (g *Gateway) resolveIntakeEndpoints() intake.EndpointInfo {
	var info intake.EndpointInfo
	if ws := strings.TrimSpace(g.wsBaseURL); ws != "" {
		httpBase := strings.Replace(strings.Replace(ws, "wss://", "https://", 1), "ws://", "http://", 1)
		info.LocalBaseURL = strings.TrimRight(httpBase, "/")
	}
	if g.pairing != nil {
		primary, _ := g.pairing.LanEndpoints()
		info.LANBaseURL = strings.TrimRight(strings.TrimSpace(primary), "/")
	}
	if extBase := g.extensionIntakeRelayBaseURL(); extBase != "" {
		info.RelayBaseURL = extBase
		return info
	}
	if g.cloudflared != nil {
		host := strings.TrimSpace(g.cloudflared.Hostname())
		lower := strings.ToLower(host)
		switch {
		case strings.HasPrefix(lower, "https://"):
			host = host[len("https://"):]
		case strings.HasPrefix(lower, "http://"):
			host = host[len("http://"):]
		}
		if i := strings.IndexByte(host, '/'); i >= 0 {
			host = host[:i]
		}
		host = strings.TrimSpace(strings.TrimRight(host, "/"))
		if host != "" {
			info.RelayBaseURL = "https://" + host
		}
	}
	return info
}

func isIntakePagePath(requestPath string) bool {
	if requestPath == "" || path.Clean(requestPath) != requestPath || !strings.HasPrefix(requestPath, "/qa/") {
		return false
	}
	tok := strings.TrimPrefix(requestPath, "/qa/")
	return tok != "" && !strings.Contains(tok, "/") && intake.IsValidPortalTokenFormat(tok)
}

func isIntakeStaticAssetPath(requestPath string) bool {
	if requestPath == "" || path.Clean(requestPath) != requestPath {
		return false
	}
	if strings.HasPrefix(requestPath, "/assets/") {
		return len(requestPath) > len("/assets/")
	}
	switch requestPath {
	case "/favicon.ico", "/favicon.svg", "/logo.svg":
		return true
	}
	return false
}

// isIntakePublicSurfaceRequest reports whether an incoming LAN (:5417) or
// reverse-proxy ingress (:5418) request targets the isolated tester portal
// rather than the paired owner controller plane.
func isIntakePublicSurfaceRequest(r *http.Request) bool {
	if r == nil || r.URL == nil || r.URL.RawPath != "" {
		return false
	}
	if intake.IsPortalAPIPath(r.URL.Path) {
		return true
	}
	if r.Method == http.MethodGet && (isIntakePagePath(r.URL.Path) || isIntakeStaticAssetPath(r.URL.Path)) {
		return true
	}
	return false
}

// serveIntakePublicSurface serves ONLY the isolated tester portal SPA shell,
// static bundle assets, and token-authenticated /api/hoxi/intake/portal/* routes.
// It never touches g.mux, OpenCode passthrough, or desktop/paired routes.
func (g *Gateway) serveIntakePublicSurface(w http.ResponseWriter, r *http.Request) {
	if g.intake == nil || !isIntakePublicSurfaceRequest(r) {
		writeJSONError(w, http.StatusForbidden, "route is not available")
		return
	}
	if intake.IsPortalAPIPath(r.URL.Path) {
		g.intake.ServePortalHTTP(w, r)
		return
	}
	if g.distFS == nil {
		http.NotFound(w, r)
		return
	}
	if isIntakePagePath(r.URL.Path) {
		data, err := g.distFS.Open("index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer data.Close()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = io.Copy(w, data)
		return
	}
	cleanAsset := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	f, err := g.distFS.Open(cleanAsset)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	st, statErr := f.Stat()
	_ = f.Close()
	if statErr != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if strings.HasPrefix(cleanAsset, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.FileServer(http.FS(g.distFS)).ServeHTTP(w, r)
}

type gatewayIntakeSessions struct {
	runner *runner.Service
	isBusy func(sessionID string) bool
}

func (b *gatewayIntakeSessions) sessionBusy(sessionID string) bool {
	if b.isBusy != nil {
		return b.isBusy(sessionID)
	}
	if b.runner == nil {
		return false
	}
	return b.runner.IsSessionBusy(sessionID)
}

func (b *gatewayIntakeSessions) EnsureSession(ctx context.Context, req intake.RunSessionRequest) (string, error) {
	if b.runner == nil {
		return "", errors.New("runner is unavailable")
	}
	if existing := strings.TrimSpace(req.SessionID); existing != "" {
		if _, ok := b.runner.Store().Get(existing); ok {
			return existing, nil
		}
	}
	if !worktree.HasCommittedHead(req.Directory) {
		return "", errors.New("intake tasks require a Git repository with at least one commit so fixes run in an isolated worktree")
	}
	provider := strings.TrimSpace(req.Provider)
	model := strings.TrimSpace(req.Model)
	runtime := ""
	if provider == "hoxi-auto" {
		provider = ""
		model = ""
	}
	if runner.IsValidRuntime(provider) {
		runtime = provider
	}
	info, _, err := b.runner.CreateConversation(ctx, runner.CreateSessionRequest{
		Title:       req.Title,
		Directory:   req.Directory,
		Runtime:     runtime,
		Mode:        req.Mode,
		ProviderID:  provider,
		ModelID:     model,
		SandboxMode: "worktree",
	}, req.Directory)
	if err != nil {
		return "", err
	}
	return info.ID, nil
}

func (b *gatewayIntakeSessions) RunTurn(ctx context.Context, sessionID string, req intake.RunSessionRequest) (string, error) {
	if b.runner == nil {
		return "", errors.New("runner is unavailable")
	}
	turn := runner.PromptRequest{
		Text:   req.Prompt,
		System: req.System,
		Agent:  strings.TrimSpace(req.Mode),
	}
	provider := strings.TrimSpace(req.Provider)
	model := strings.TrimSpace(req.Model)
	if provider != "" && provider != "hoxi-auto" && model != "" {
		turn.Model = &runner.PromptModel{ProviderID: provider, ModelID: model}
	}
	if len(req.Attachments) > 0 {
		parts := make([]runner.MessagePart, 0, len(req.Attachments))
		for _, a := range req.Attachments {
			parts = append(parts, runner.MessagePart{
				Type:     "file",
				Mime:     a.Mime,
				Filename: a.Filename,
				URL:      "/api/hoxi/attachments/" + a.ID + "/content",
			})
		}
		turn.Parts = parts
	}
	return b.runner.RunPromptAndWait(ctx, sessionID, turn)
}

func (b *gatewayIntakeSessions) SanitizedTimeline(sessionID string) (intake.SanitizedTimeline, bool) {
	if b.runner == nil {
		return intake.SanitizedTimeline{}, false
	}
	var turns []intake.SanitizedTurn
	found := b.runner.Store().VisitTurns(sessionID, func(rec runner.SessionRecord) {
		sawTurn := false
		for _, turn := range rec.Turns {
			if turn.Info.Role == "user" {
				if sawTurn && !isTesterIntakeUserTurn(turn) {
					noticeTurn := intake.SanitizedTurn{
						ID:        turn.Info.ID,
						CreatedAt: turn.Info.Time.Created,
						Steps: []intake.SanitizedStep{{
							ID:       turn.Info.ID + "_dev_followup",
							Kind:     "notice",
							Category: "developer_followup",
						}},
					}
					if n := len(turns); n > 0 && isDeveloperFollowupNoticeTurn(turns[n-1]) {
						turns[n-1] = noticeTurn
					} else {
						turns = append(turns, noticeTurn)
					}
				}
				sawTurn = true
				continue
			}
			if turn.Info.Role != "assistant" {
				continue
			}
			sawTurn = true
			var steps []intake.SanitizedStep
			for _, part := range turn.Parts {
				switch part.Type {
				case "tool":
					if part.State == nil {
						continue
					}
					cat, label, todos := intake.ClassifyAndLabelTool(part.Tool, part.State.Input)
					step := intake.SanitizedStep{
						ID:       part.ID,
						Kind:     "tool",
						Category: cat,
						Label:    label,
						Status:   part.State.Status,
					}
					if cat == "todo" {
						step.Kind = "todo"
						step.Todos = todos
					}
					if cat == "edit" {
						files, adds, dels := intake.ExtractToolFileChanges(part.Tool, part.State.Input, part.State.Metadata)
						if label == "修改项目文件" && len(files) > 0 {
							if len(files) > 1 {
								step.Label = fmt.Sprintf("修改 %s 等 %d 个文件", files[0].File, len(files))
							} else if files[0].File != "" {
								step.Label = "修改 " + files[0].File
							}
						}
						if part.State.Status != "error" {
							step.Additions = adds
							step.Deletions = dels
							if len(files) > 0 {
								step.Files = files
							}
						}
					}
					if part.State.Time != nil {
						step.StartTime = part.State.Time.Start
						step.EndTime = part.State.Time.End
					} else if part.Time != nil {
						step.StartTime = part.Time.Start
						step.EndTime = part.Time.End
					}
					steps = append(steps, step)
				case "text":
					cleaned := intake.SanitizeAssistantText(part.Text)
					if cleaned != "" {
						steps = append(steps, intake.SanitizedStep{
							ID:   part.ID,
							Kind: "text",
							Text: cleaned,
						})
					}
				}
			}
			if len(steps) > 0 {
				turns = append(turns, intake.SanitizedTurn{
					ID:          turn.Info.ID,
					CreatedAt:   turn.Info.Time.Created,
					CompletedAt: turn.Info.Time.Completed,
					Steps:       steps,
				})
			}
		}
	})
	if !found {
		return intake.SanitizedTimeline{}, false
	}
	if turns == nil {
		turns = []intake.SanitizedTurn{}
	}
	return intake.SanitizedTimeline{
		Busy:  b.runner.IsSessionBusy(sessionID),
		Turns: turns,
	}, true
}

func (b *gatewayIntakeSessions) SubscribeSession(sessionID string) (<-chan struct{}, func()) {
	if b.runner == nil {
		return nil, func() {}
	}
	rawCh, unsub := b.runner.Subscribe(sessionID)
	notify := make(chan struct{}, 1)
	stop := make(chan struct{})
	var once sync.Once
	clean := func() {
		once.Do(func() {
			close(stop)
			unsub()
		})
	}
	go func() {
		for {
			select {
			case <-stop:
				return
			case _, ok := <-rawCh:
				if !ok {
					return
				}
				select {
				case notify <- struct{}{}:
				default:
				}
			}
		}
	}()
	return notify, clean
}

func (b *gatewayIntakeSessions) Abort(sessionID string) {
	if b.runner != nil {
		b.runner.AbortSession(sessionID)
	}
}

func isTesterIntakeUserTurn(turn runner.MessageTurn) bool {
	var texts []string
	for _, part := range turn.Parts {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			texts = append(texts, part.Text)
		}
	}
	return intake.IsTesterReportPrompt(strings.Join(texts, "\n"))
}

func isDeveloperFollowupNoticeTurn(turn intake.SanitizedTurn) bool {
	return len(turn.Steps) == 1 &&
		turn.Steps[0].Kind == "notice" &&
		turn.Steps[0].Category == "developer_followup"
}

func formatRunnerMessageError(e *runner.MessageError) string {
	if e == nil {
		return ""
	}
	if e.Data != nil && strings.TrimSpace(e.Data.Message) != "" {
		if strings.TrimSpace(e.Name) != "" {
			return strings.TrimSpace(e.Name) + ": " + strings.TrimSpace(e.Data.Message)
		}
		return strings.TrimSpace(e.Data.Message)
	}
	return strings.TrimSpace(e.Name)
}

func extractRunnerEventSessionID(evt runner.SSEEvent) string {
	if evt.Properties == nil {
		return ""
	}
	if sid, ok := evt.Properties["sessionID"].(string); ok && strings.TrimSpace(sid) != "" {
		return strings.TrimSpace(sid)
	}
	switch info := evt.Properties["info"].(type) {
	case runner.MessageInfo:
		if strings.TrimSpace(info.SessionID) != "" {
			return strings.TrimSpace(info.SessionID)
		}
	case *runner.MessageInfo:
		if info != nil && strings.TrimSpace(info.SessionID) != "" {
			return strings.TrimSpace(info.SessionID)
		}
	case runner.SessionInfo:
		if strings.TrimSpace(info.ID) != "" {
			return strings.TrimSpace(info.ID)
		}
	case *runner.SessionInfo:
		if info != nil && strings.TrimSpace(info.ID) != "" {
			return strings.TrimSpace(info.ID)
		}
	}
	switch part := evt.Properties["part"].(type) {
	case runner.MessagePart:
		if strings.TrimSpace(part.SessionID) != "" {
			return strings.TrimSpace(part.SessionID)
		}
	case *runner.MessagePart:
		if part != nil && strings.TrimSpace(part.SessionID) != "" {
			return strings.TrimSpace(part.SessionID)
		}
	}
	return ""
}

func extractRunnerSessionStatusType(evt runner.SSEEvent) string {
	if evt.Properties == nil {
		return ""
	}
	switch st := evt.Properties["status"].(type) {
	case runner.SessionStatus:
		return strings.ToLower(strings.TrimSpace(st.Type))
	case *runner.SessionStatus:
		if st != nil {
			return strings.ToLower(strings.TrimSpace(st.Type))
		}
	case map[string]interface{}:
		if typ, ok := st["type"].(string); ok {
			return strings.ToLower(strings.TrimSpace(typ))
		}
	}
	return ""
}

func extractRunnerSessionError(raw interface{}) string {
	switch e := raw.(type) {
	case runner.MessageError:
		return formatRunnerMessageError(&e)
	case *runner.MessageError:
		return formatRunnerMessageError(e)
	case string:
		return strings.TrimSpace(e)
	case map[string]interface{}:
		name, _ := e["name"].(string)
		if data, ok := e["data"].(map[string]interface{}); ok {
			if msg, ok := data["message"].(string); ok && strings.TrimSpace(msg) != "" {
				if strings.TrimSpace(name) != "" {
					return strings.TrimSpace(name) + ": " + strings.TrimSpace(msg)
				}
				return strings.TrimSpace(msg)
			}
		}
		if msg, ok := e["message"].(string); ok && strings.TrimSpace(msg) != "" {
			return strings.TrimSpace(msg)
		}
		return strings.TrimSpace(name)
	}
	return ""
}

func (b *gatewayIntakeSessions) inspectLatestTurn(sessionID string) (summary string, completed bool, aborted bool, errText string) {
	if b.runner == nil {
		return "", false, false, ""
	}
	b.runner.Store().VisitTurns(sessionID, func(rec runner.SessionRecord) {
		for i := len(rec.Turns) - 1; i >= 0; i-- {
			turn := rec.Turns[i]
			if turn.Info.Role == "user" {
				return
			}
			if turn.Info.Role != "assistant" {
				continue
			}
			if turn.Info.Error != nil && errText == "" {
				errText = formatRunnerMessageError(turn.Info.Error)
			}
			if strings.EqualFold(strings.TrimSpace(turn.Info.Finish), "aborted") {
				aborted = true
			} else if turn.Info.Time.Completed > 0 {
				completed = true
			}
			var texts []string
			for _, part := range turn.Parts {
				if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
					texts = append(texts, part.Text)
				}
			}
			if len(texts) > 0 && summary == "" {
				summary = strings.Join(texts, "\n")
			}
			if summary != "" {
				if !aborted {
					completed = true
				}
				return
			}
		}
	})
	return summary, completed, aborted, errText
}

func (b *gatewayIntakeSessions) startSessionWatcher(svc *intake.Service) func() {
	if b.runner == nil || svc == nil {
		return func() {}
	}
	rawCh, unsub := b.runner.Subscribe("")
	stop := make(chan struct{})
	var once sync.Once
	clean := func() {
		once.Do(func() {
			close(stop)
			unsub()
		})
	}
	go func() {
		for {
			select {
			case <-stop:
				return
			case evt, ok := <-rawCh:
				if !ok {
					return
				}
				b.forwardRunnerEvent(svc, evt)
			}
		}
	}()
	return clean
}

func (b *gatewayIntakeSessions) forwardRunnerEvent(svc *intake.Service, evt runner.SSEEvent) {
	if evt.Type == "hoxi.stream.resync" {
		for _, sid := range svc.LinkedSessionIDs() {
			if b.sessionBusy(sid) {
				svc.HandleSessionEvent(intake.SessionEvent{
					SessionID: sid,
					Kind:      intake.SessionEventBusy,
				})
				continue
			}
			summary, completed, aborted, errText := b.inspectLatestTurn(sid)
			svc.HandleSessionEvent(intake.SessionEvent{
				SessionID: sid,
				Kind:      intake.SessionEventIdle,
				Summary:   summary,
				Completed: completed,
				Aborted:   aborted,
				Error:     errText,
			})
		}
		return
	}
	sid := extractRunnerEventSessionID(evt)
	if sid == "" || !svc.HasLinkedSession(sid) {
		return
	}
	switch evt.Type {
	case "session.status":
		switch extractRunnerSessionStatusType(evt) {
		case "busy", "retry":
			if !b.sessionBusy(sid) {
				return
			}
			svc.HandleSessionEvent(intake.SessionEvent{
				SessionID: sid,
				Kind:      intake.SessionEventBusy,
			})
		case "idle":
			if b.sessionBusy(sid) {
				svc.HandleSessionEvent(intake.SessionEvent{
					SessionID: sid,
					Kind:      intake.SessionEventProgress,
				})
				return
			}
			summary, completed, aborted, errText := b.inspectLatestTurn(sid)
			svc.HandleSessionEvent(intake.SessionEvent{
				SessionID: sid,
				Kind:      intake.SessionEventIdle,
				Summary:   summary,
				Completed: completed,
				Aborted:   aborted,
				Error:     errText,
			})
		}
	case "session.idle":
		if b.sessionBusy(sid) {
			svc.HandleSessionEvent(intake.SessionEvent{
				SessionID: sid,
				Kind:      intake.SessionEventProgress,
			})
			return
		}
		summary, completed, aborted, errText := b.inspectLatestTurn(sid)
		svc.HandleSessionEvent(intake.SessionEvent{
			SessionID: sid,
			Kind:      intake.SessionEventIdle,
			Summary:   summary,
			Completed: completed,
			Aborted:   aborted,
			Error:     errText,
		})
	case "session.error":
		summary, completed, aborted, storeErr := b.inspectLatestTurn(sid)
		errText := extractRunnerSessionError(evt.Properties["error"])
		if errText == "" {
			errText = storeErr
		}
		svc.HandleSessionEvent(intake.SessionEvent{
			SessionID: sid,
			Kind:      intake.SessionEventError,
			Summary:   summary,
			Completed: completed,
			Aborted:   aborted,
			Error:     errText,
		})
	case "message.updated", "message.part.updated", "message.part.delta", "session.updated":
		svc.HandleSessionEvent(intake.SessionEvent{
			SessionID: sid,
			Kind:      intake.SessionEventProgress,
		})
	}
}
