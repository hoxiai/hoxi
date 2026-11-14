package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/hoxiai/hoxi/internal/workcatalog"
)

type workCatalogBrowserExtractRequest struct {
	SessionID string `json:"sessionID"`
	Directory string `json:"directory,omitempty"`
	Operation string `json:"operation,omitempty"`
}

type workCatalogBrowserFramePayload struct {
	HasCaptcha  bool                              `json:"hasCaptcha"`
	SellerID    string                            `json:"sellerId,omitempty"`
	HasNextPage bool                              `json:"hasNextPage"`
	PageURL     string                            `json:"pageUrl,omitempty"`
	Items       []workcatalog.BrowserProductInput `json:"items"`
}

type workCatalogBrowserExtractResponse struct {
	Running     bool                              `json:"running"`
	ActiveURL   string                            `json:"activeUrl,omitempty"`
	ActiveTitle string                            `json:"activeTitle,omitempty"`
	TabCount    int                               `json:"tabCount"`
	HasCaptcha  bool                              `json:"hasCaptcha"`
	SellerID    string                            `json:"sellerId,omitempty"`
	HasNextPage bool                              `json:"hasNextPage"`
	Clicked     bool                              `json:"clicked,omitempty"`
	Items       []workcatalog.BrowserProductInput `json:"items"`
}

func parseFrameExtractPayload(raw string) (workCatalogBrowserFramePayload, bool) {
	var out workCatalogBrowserFramePayload
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "undefined" || trimmed == "null" {
		return out, false
	}
	if strings.HasPrefix(trimmed, "\"") {
		var unwrapped string
		if json.Unmarshal([]byte(trimmed), &unwrapped) == nil {
			trimmed = strings.TrimSpace(unwrapped)
		}
	}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		return out, false
	}
	return out, true
}

func parseFrameBoolPayload(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "true" {
		return true
	}
	if strings.HasPrefix(trimmed, "\"") {
		var unwrapped string
		if json.Unmarshal([]byte(trimmed), &unwrapped) == nil {
			return strings.TrimSpace(unwrapped) == "true"
		}
	}
	return false
}

func hasEnrichedDetail(item workcatalog.BrowserProductInput) bool {
	if item.Data == nil {
		return false
	}
	desc, _ := item.Data["description"].(string)
	return strings.TrimSpace(desc) != ""
}

func (g *Gateway) resolveCatalogBrowserSession(rawSessionID, storeID string) (string, error) {
	storeID = strings.TrimSpace(storeID)
	if storeID == "" {
		return "", errors.New("storeId is required")
	}
	canonicalScope := "workstore_" + storeID
	sessionID := strings.TrimSpace(rawSessionID)
	if sessionID == "" {
		return canonicalScope, nil
	}
	if len(sessionID) > 128 || strings.ContainsAny(sessionID, "/\\") {
		return "", errors.New("invalid browser session")
	}
	if sessionID == canonicalScope {
		return sessionID, nil
	}
	if g.workApps != nil {
		storeProf := strings.TrimSpace(g.workApps.ResolveBrowserProfileKey(canonicalScope))
		reqProf := strings.TrimSpace(g.workApps.ResolveBrowserProfileKey(sessionID))
		if storeProf != "" && reqProf != "" && reqProf == storeProf {
			return sessionID, nil
		}
		matched := false
		g.walkWorkSessionAncestors(sessionID, func(cur string) bool {
			if prof := g.workApps.ResolveSessionStoreProfile(cur); prof != nil && prof.ID == storeID {
				matched = true
				return true
			}
			return false
		})
		if matched {
			return sessionID, nil
		}
	}
	return "", errors.New("browser session does not belong to the requested store")
}

func (g *Gateway) handleWorkCatalogBrowserExtract(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	target, err := g.resolveWorkCatalogUI(r)
	if err != nil {
		writeJSONError(w, http.StatusForbidden, err.Error())
		return
	}
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	mediaType, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" {
		writeJSONError(w, http.StatusBadRequest, "application/json is required")
		return
	}
	var req workCatalogBrowserExtractRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid browser extract request")
		return
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		writeJSONError(w, http.StatusBadRequest, "one JSON object is required")
		return
	}

	resolvedSessionID, sessionErr := g.resolveCatalogBrowserSession(req.SessionID, target.Scope.StoreID)
	if sessionErr != nil {
		writeJSONError(w, http.StatusForbidden, sessionErr.Error())
		return
	}

	if g.browser == nil {
		_ = json.NewEncoder(w).Encode(workCatalogBrowserExtractResponse{
			Running: false,
			Items:   []workcatalog.BrowserProductInput{},
		})
		return
	}

	op := strings.TrimSpace(req.Operation)
	if op == "" {
		op = "extract"
	}

	switch op {
	case "next_page":
		evals, st, evalErr := g.browser.EvaluateHostScript(
			r.Context(),
			resolvedSessionID,
			req.Directory,
			target.Scope.StoreID,
			catalog1688NextPageScript,
			false,
		)
		if evalErr != nil {
			writeJSONError(w, http.StatusBadGateway, evalErr.Error())
			return
		}
		clicked := false
		for _, ev := range evals {
			if parseFrameBoolPayload(ev.Value) {
				clicked = true
				break
			}
		}
		_ = json.NewEncoder(w).Encode(workCatalogBrowserExtractResponse{
			Running:     len(st.Tabs) > 0,
			ActiveURL:   st.URL,
			ActiveTitle: st.Title,
			TabCount:    len(st.Tabs),
			Clicked:     clicked,
			Items:       []workcatalog.BrowserProductInput{},
		})
		return

	case "extract":
		evals, st, evalErr := g.browser.EvaluateHostScript(
			r.Context(),
			resolvedSessionID,
			req.Directory,
			target.Scope.StoreID,
			catalog1688ExtractScript,
			true,
		)
		if evalErr != nil {
			writeJSONError(w, http.StatusBadGateway, evalErr.Error())
			return
		}
		resp := workCatalogBrowserExtractResponse{
			Running:     len(st.Tabs) > 0,
			ActiveURL:   st.URL,
			ActiveTitle: st.Title,
			TabCount:    len(st.Tabs),
			Items:       []workcatalog.BrowserProductInput{},
		}
		if len(evals) == 0 {
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		byID := make(map[string]int)
		activeCaptcha := false
		anyCaptcha := false

		for _, ev := range evals {
			payload, ok := parseFrameExtractPayload(ev.Value)
			if !ok {
				continue
			}
			if payload.HasCaptcha {
				anyCaptcha = true
				if ev.TabID == st.TabID {
					activeCaptcha = true
				}
			}
			if resp.SellerID == "" && strings.TrimSpace(payload.SellerID) != "" {
				resp.SellerID = strings.TrimSpace(payload.SellerID)
			}
			if ev.TabID == st.TabID && payload.HasNextPage {
				resp.HasNextPage = true
			}
			for _, item := range payload.Items {
				id := strings.TrimSpace(item.ID)
				if id == "" || item.Data == nil {
					continue
				}
				if existingIdx, exists := byID[id]; exists {
					if !hasEnrichedDetail(resp.Items[existingIdx]) && hasEnrichedDetail(item) {
						resp.Items[existingIdx] = item
					}
					continue
				}
				byID[id] = len(resp.Items)
				resp.Items = append(resp.Items, item)
				if len(resp.Items) >= 200 {
					break
				}
			}
			if len(resp.Items) >= 200 {
				break
			}
		}

		if len(resp.Items) == 0 && (activeCaptcha || anyCaptcha) {
			resp.HasCaptcha = true
		}
		_ = json.NewEncoder(w).Encode(resp)
		return

	default:
		writeJSONError(w, http.StatusBadRequest, "unsupported browser extract operation")
	}
}
