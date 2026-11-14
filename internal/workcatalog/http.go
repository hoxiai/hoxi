package workcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"mime"
	"net/http"
	"strconv"
)

type HTTPHandler struct {
	service   *Service
	resolve   func(*http.Request) (Target, error)
	authorize func(*http.Request) (Authorize, error)
}

func NewHTTPHandler(service *Service, resolve func(*http.Request) (Target, error), authorize func(*http.Request) (Authorize, error)) *HTTPHandler {
	return &HTTPHandler{service: service, resolve: resolve, authorize: authorize}
}

type CatalogStatus struct {
	StoreID       string              `json:"storeId"`
	Capabilities  Capabilities        `json:"capabilities"`
	Authorization AuthorizationStatus `json:"authorization"`
	ProductCount  int                 `json:"productCount"`
	LastSync      *SyncResult         `json:"lastSync,omitempty"`
	Job           *SyncJob            `json:"job,omitempty"`
}

func (s *Service) Status(ctx context.Context, target Target) (CatalogStatus, error) {
	out := CatalogStatus{StoreID: target.Scope.StoreID, Capabilities: target.Capabilities, Authorization: target.Authorization}
	if !target.Capabilities.Supported {
		return out, nil
	}
	st, err := s.store.load(ctx, target.Scope)
	if err != nil {
		return out, err
	}
	out.ProductCount, out.LastSync = len(st.Products), st.LastSync
	out.Job, err = s.syncJobView(target.Scope, st.SyncJob)
	return out, err
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	target, err := h.resolve(r)
	if err != nil {
		catalogError(w, http.StatusForbidden, err)
		return
	}
	var output any
	scope := target.Scope
	ctx := r.Context()
	method := r.Method
	path := r.URL.Path
	switch {
	case path == "/api/hoxi/work/catalog/status" && method == http.MethodGet:
		output, err = h.service.Status(ctx, target)
	case path == "/api/hoxi/work/catalog/authorization" && method == http.MethodPut:
		var input Credentials
		if err = decodeCatalogJSON(w, r, &input); err == nil {
			if !target.Capabilities.Supported {
				err = errors.New("this platform has no local catalog adapter")
			} else {
				output, err = h.service.ConfigureAuthorizationForPlatform(ctx, target.PlatformID, scope.StoreID, input)
			}
		}
	case path == "/api/hoxi/work/catalog/browser-auth" && method == http.MethodPut:
		var input BrowserAuthorizationInput
		if err = decodeCatalogJSON(w, r, &input); err == nil {
			if !target.Capabilities.Supported {
				err = errors.New("this platform has no local catalog adapter")
			} else {
				output, err = h.service.ConfigureBrowserAuthorizationForPlatform(ctx, target.PlatformID, scope.StoreID, input)
			}
		}
	case path == "/api/hoxi/work/catalog/browser-sync" && method == http.MethodPost:
		var input BrowserSyncInput
		if err = decodeCatalogJSON(w, r, &input); err == nil {
			output, err = h.service.SyncBrowserProducts(ctx, target, input)
		}
	case path == "/api/hoxi/work/catalog/authorization-check" && method == http.MethodPost:
		var input struct{}
		if err = decodeCatalogJSON(w, r, &input); err == nil {
			output, err = h.service.CheckAuthorization(ctx, target)
		}
	case path == "/api/hoxi/work/catalog/products" && method == http.MethodGet:
		var query Query
		query.Query, query.Status, query.Snapshot = r.URL.Query().Get("query"), r.URL.Query().Get("status"), r.URL.Query().Get("snapshot")
		query.HasDraft = r.URL.Query().Get("hasDraft") == "true"
		query.Offset, err = catalogInt(r.URL.Query().Get("offset"), 0)
		if err == nil {
			query.Limit, err = catalogInt(r.URL.Query().Get("limit"), 20)
		}
		if err == nil {
			output, err = h.service.QueryProducts(ctx, scope, query)
		}
	case path == "/api/hoxi/work/catalog/product" && method == http.MethodGet:
		var product Product
		product, err = h.service.GetProduct(ctx, scope, r.URL.Query().Get("productId"))
		product.Data = visibleCatalogMap(product.Data)
		output = product
	case path == "/api/hoxi/work/catalog/drafts" && method == http.MethodGet:
		output, err = h.service.QueryDrafts(ctx, scope)
	case path == "/api/hoxi/work/catalog/drafts" && method == http.MethodPost:
		var input struct {
			Kind       string           `json:"kind"`
			ProductID  string           `json:"productId,omitempty"`
			Changes    Patch            `json:"changes,omitempty"`
			NewProduct *NewProductInput `json:"newProduct,omitempty"`
		}
		if err = decodeCatalogJSON(w, r, &input); err == nil {
			if !target.Capabilities.EditDraft || scope.SellerLoginID == "unconfigured" {
				err = errors.New("configure the seller identity before creating drafts")
			} else if input.Kind == "create" && input.NewProduct != nil && input.ProductID == "" && input.Changes.Subject == nil && input.Changes.Description == nil && input.Changes.Images == nil {
				output, err = h.service.IntakeDraft(ctx, scope, *input.NewProduct)
			} else if input.Kind == "update" && input.ProductID != "" && input.NewProduct == nil {
				output, err = h.service.CreateDraftForPlatform(ctx, target.PlatformID, scope, input.ProductID, input.Changes)
			} else {
				err = errors.New("draft kind and payload do not match")
			}
		}
	case path == "/api/hoxi/work/catalog/draft" && method == http.MethodGet:
		var draft Draft
		draft, err = h.service.GetDraft(ctx, scope, r.URL.Query().Get("draftId"))
		draft.Base.Data = visibleCatalogMap(draft.Base.Data)
		output = draft
	case path == "/api/hoxi/work/catalog/draft" && method == http.MethodDelete:
		var input revisionInput
		if err = decodeCatalogJSON(w, r, &input); err == nil {
			output, err = h.service.DiscardDraft(ctx, scope, input.DraftID, input.Revision)
		}
	case path == "/api/hoxi/work/catalog/sync" && method == http.MethodPost:
		var input SyncInput
		if err = decodeCatalogJSON(w, r, &input); err == nil {
			output, err = h.service.StartSync(ctx, target, input)
		}
	case path == "/api/hoxi/work/catalog/sync" && method == http.MethodDelete:
		err = h.service.CancelSync(scope.StoreID)
		output = map[string]bool{"ok": err == nil}
	case path == "/api/hoxi/work/catalog/publish" && method == http.MethodPost:
		var input revisionInput
		if err = decodeCatalogJSON(w, r, &input); err == nil {
			var authorize Authorize
			authorize, err = h.authorize(r)
			if err == nil {
				var api Platform
				api, err = target.Platform()
				if err == nil {
					output, err = h.service.Publish(ctx, scope, api, input.DraftID, input.Revision, authorize)
				}
			}
		}
	case path == "/api/hoxi/work/catalog/verify" && method == http.MethodPost:
		var input draftInput
		if err = decodeCatalogJSON(w, r, &input); err == nil {
			var api Platform
			api, err = target.Platform()
			if err == nil {
				output, err = h.service.Verify(ctx, scope, api, input.DraftID)
			}
		}
	default:
		catalogError(w, http.StatusMethodNotAllowed, errors.New("unsupported catalog route or method"))
		return
	}
	if err != nil {
		catalogError(w, http.StatusBadRequest, err)
		return
	}
	_ = json.NewEncoder(w).Encode(output)
}

func decodeCatalogJSON(w http.ResponseWriter, r *http.Request, output any) error {
	mediaType, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" {
		return errors.New("application/json is required")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(output) != nil {
		return errors.New("invalid catalog request")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("one JSON object is required")
	}
	return nil
}

func catalogInt(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errors.New("invalid catalog pagination")
	}
	return value, nil
}

func catalogError(w http.ResponseWriter, status int, err error) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// Preserve 64-bit platform IDs when JavaScript reads the UI response, without
// changing the original numeric values in the on-disk platform snapshot.
func visibleCatalogMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = visibleCatalogValue(value)
	}
	return output
}

func visibleCatalogValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return visibleCatalogMap(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = visibleCatalogValue(item)
		}
		return out
	case json.Number:
		if number, err := v.Float64(); err == nil && math.Abs(number) > 9007199254740991 {
			return string(v)
		}
	}
	return value
}
