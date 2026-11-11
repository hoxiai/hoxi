package ecom

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// HTTPHandler serves deterministic e-commerce operational APIs under
// /api/hoxi/work/ecom/{profit,ads-audit,listing-check,shopify-csv}.
type HTTPHandler struct {
	stores StoreLookup
	sheets SpreadsheetIO
}

func NewHTTPHandler(stores StoreLookup, sheets SpreadsheetIO) *HTTPHandler {
	return &HTTPHandler{stores: stores, sheets: sheets}
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, `{"error":"application/json required"}`, http.StatusUnsupportedMediaType)
		return
	}

	switch r.URL.Path {
	case "/api/hoxi/work/ecom/profit":
		var req ProfitRequest
		if !decodeJSONBody(w, r, &req) {
			return
		}
		res, err := CalculateProfit(req, h.resolveDefaults(req.StoreID))
		if err != nil {
			writeHTTPError(w, http.StatusBadRequest, err)
			return
		}
		_ = json.NewEncoder(w).Encode(res)

	case "/api/hoxi/work/ecom/listing-check":
		var req ListingCheckRequest
		if !decodeJSONBody(w, r, &req) {
			return
		}
		res, err := CheckListing(req, h.resolveDefaults(req.StoreID))
		if err != nil {
			writeHTTPError(w, http.StatusBadRequest, err)
			return
		}
		_ = json.NewEncoder(w).Encode(res)

	case "/api/hoxi/work/ecom/ads-audit":
		var req AdsAuditRequest
		if !decodeJSONBody(w, r, &req) {
			return
		}
		wsDir, err := requireWorkspaceDir(r, req.WorkspaceDir)
		if err != nil {
			writeHTTPError(w, http.StatusBadRequest, err)
			return
		}
		req.WorkspaceDir = wsDir
		res, err := AuditAdsReport(req, h.resolveDefaults(req.StoreID), h.sheets)
		if err != nil {
			writeHTTPError(w, http.StatusBadRequest, err)
			return
		}
		_ = json.NewEncoder(w).Encode(res)

	case "/api/hoxi/work/ecom/shopify-csv":
		var req ShopifyCSVRequest
		if !decodeJSONBody(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.InputCSVPath) != "" || strings.TrimSpace(req.OutputCSVPath) != "" {
			wsDir, err := requireWorkspaceDir(r, req.WorkspaceDir)
			if err != nil {
				writeHTTPError(w, http.StatusBadRequest, err)
				return
			}
			req.WorkspaceDir = wsDir
		}
		res, err := BuildOrValidateShopifyCSV(req, h.sheets)
		if err != nil {
			writeHTTPError(w, http.StatusBadRequest, err)
			return
		}
		_ = json.NewEncoder(w).Encode(res)

	default:
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}
}

func (h *HTTPHandler) resolveDefaults(storeID string) *StoreDefaults {
	storeID = strings.TrimSpace(storeID)
	if h == nil || h.stores == nil || storeID == "" {
		return nil
	}
	if d, ok := h.stores.LookupStoreDefaults(storeID); ok {
		return &d
	}
	return nil
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		writeHTTPError(w, http.StatusBadRequest, errors.New("invalid json body"))
		return false
	}
	return true
}

func requireWorkspaceDir(r *http.Request, bodyDir string) (string, error) {
	dir := strings.TrimSpace(r.URL.Query().Get("directory"))
	if dir == "" {
		dir = strings.TrimSpace(bodyDir)
	}
	if dir == "" || !filepath.IsAbs(dir) {
		return "", errors.New("an absolute workspace directory (?directory=...) is required for file operations")
	}
	cleaned := filepath.Clean(dir)
	info, err := os.Stat(cleaned)
	if err != nil || !info.IsDir() {
		return "", errors.New("workspace directory does not exist")
	}
	return cleaned, nil
}

func writeHTTPError(w http.ResponseWriter, code int, err error) {
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
