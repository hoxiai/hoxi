package workcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var thumbnailSizeSuffix = regexp.MustCompile(`\.\d{2,4}x\d{2,4}(?:xz|q\d+)?\.`)

type BrowserProductInput struct {
	ID   string         `json:"id"`
	Data map[string]any `json:"data"`
}

type BrowserSyncInput struct {
	SellerLoginID string                `json:"sellerLoginId,omitempty"`
	Products      []BrowserProductInput `json:"products"`
	ReplaceAll    bool                  `json:"replaceAll,omitempty"`
}

type BrowserSyncResult struct {
	Authorization AuthorizationStatus `json:"authorization"`
	Sync          SyncResult          `json:"sync"`
	ProductCount  int                 `json:"productCount"`
}

// SyncBrowserProducts ingests seller-owned product records extracted from Hoxi's
// store-isolated browser into the local catalog snapshot, preserving 64-bit IDs,
// owner isolation, and previously enriched detail fields during list refreshes.
func (s *Service) SyncBrowserProducts(ctx context.Context, target Target, in BrowserSyncInput) (BrowserSyncResult, error) {
	if !target.Capabilities.Supported {
		return BrowserSyncResult{}, errors.New("this platform has no local catalog adapter")
	}
	if len(in.Products) == 0 || len(in.Products) > 200 {
		return BrowserSyncResult{}, errors.New("browser sync batch must contain 1..200 products")
	}

	authView := target.Authorization
	scope := target.Scope
	seller := strings.TrimSpace(in.SellerLoginID)
	if scope.SellerLoginID == "unconfigured" {
		if seller == "" {
			return BrowserSyncResult{}, errors.New("seller login ID is required before syncing browser products")
		}
		bound, err := s.ConfigureBrowserAuthorizationForPlatform(ctx, target.PlatformID, scope.StoreID, BrowserAuthorizationInput{
			SellerLoginID: seller,
		})
		if err != nil {
			return BrowserSyncResult{}, err
		}
		authView = bound
		scope.SellerLoginID = bound.SellerLoginID
	} else if seller != "" && seller != scope.SellerLoginID {
		return BrowserSyncResult{}, errors.New("browser seller login ID does not match the bound store")
	}

	now := time.Now().UTC()
	seen := make(map[string]struct{}, len(in.Products))
	prepared := make([]Product, 0, len(in.Products))
	for _, rawItem := range in.Products {
		p, err := normalizeBrowserProduct(rawItem, scope.SellerLoginID, now)
		if err != nil {
			return BrowserSyncResult{}, err
		}
		if _, dup := seen[p.ID]; dup {
			return BrowserSyncResult{}, fmt.Errorf("duplicate product %s in browser sync batch", p.ID)
		}
		seen[p.ID] = struct{}{}
		prepared = append(prepared, p)
	}

	unlock, err := s.store.lock(ctx, scope, ".operation.lock")
	if err != nil {
		return BrowserSyncResult{}, err
	}
	defer unlock()

	syncID, err := newID("bsyn_")
	if err != nil {
		return BrowserSyncResult{}, err
	}
	var count int
	var syncRes SyncResult
	err = s.store.update(ctx, scope, func(st *state) error {
		if in.ReplaceAll {
			next := make(map[string]Product, len(prepared))
			for _, incoming := range prepared {
				if existing, ok := st.Products[incoming.ID]; ok {
					next[incoming.ID] = mergeBrowserProduct(existing, incoming)
				} else {
					next[incoming.ID] = incoming
				}
			}
			st.Products = next
		} else {
			for _, incoming := range prepared {
				if existing, ok := st.Products[incoming.ID]; ok {
					st.Products[incoming.ID] = mergeBrowserProduct(existing, incoming)
				} else {
					st.Products[incoming.ID] = incoming
				}
			}
		}
		count = len(st.Products)
		syncRes = SyncResult{
			ID:         syncID,
			Status:     "completed",
			Fetched:    len(prepared),
			Total:      count,
			StartedAt:  now,
			FinishedAt: now,
		}
		st.LastSync = &syncRes
		return nil
	})
	if err != nil {
		return BrowserSyncResult{}, err
	}
	return BrowserSyncResult{
		Authorization: authView,
		Sync:          syncRes,
		ProductCount:  count,
	}, nil
}

func normalizeBrowserProduct(raw BrowserProductInput, seller string, syncedAt time.Time) (Product, error) {
	id := strings.TrimSpace(raw.ID)
	if !productIDPattern.MatchString(id) {
		return Product{}, errors.New("invalid platform product ID")
	}
	if raw.Data == nil {
		return Product{}, errors.New("product data is required")
	}
	encoded, err := json.Marshal(raw.Data)
	if err != nil || len(encoded) > 1<<20 {
		return Product{}, errors.New("invalid or oversized browser product payload")
	}
	var data map[string]any
	dec := json.NewDecoder(bytes.NewReader(encoded))
	dec.UseNumber()
	if err := dec.Decode(&data); err != nil || data == nil {
		return Product{}, errors.New("invalid browser product JSON object")
	}
	if existingID := scalarID(data["productID"]); existingID == "" {
		data["productID"] = id
	} else if existingID != id {
		return Product{}, errors.New("productID in data does not match product ID")
	}
	owner, _ := data["sellerLoginId"].(string)
	if owner == "" {
		owner, _ = data["supplierLoginId"].(string)
	}
	if owner == "" {
		data["sellerLoginId"] = seller
	}
	subject, _ := data["subject"].(string)
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return Product{}, fmt.Errorf("product %s is missing subject", id)
	}
	data["subject"] = subject
	if status, _ := data["status"].(string); strings.TrimSpace(status) == "" {
		data["status"] = "published"
	}
	if err := sanitizeBrowserProductImages(data); err != nil {
		return Product{}, err
	}
	p := Product{ID: id, Data: data, SyncedAt: syncedAt}
	if err := validateOwnedProduct(p, seller); err != nil {
		return Product{}, err
	}
	return p, nil
}

func sanitizeBrowserProductImages(data map[string]any) error {
	rawImage, exists := data["image"]
	if !exists || rawImage == nil {
		return nil
	}
	imgMap, ok := rawImage.(map[string]any)
	if !ok {
		return errors.New("product image field must be an object")
	}
	rawList, exists := imgMap["images"]
	if !exists || rawList == nil {
		imgMap["images"] = []string{}
		return nil
	}
	encoded, err := json.Marshal(rawList)
	if err != nil {
		return errors.New("invalid product images array")
	}
	var candidates []string
	if err := json.Unmarshal(encoded, &candidates); err != nil {
		return errors.New("product images must be an array of strings")
	}
	cleaned := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, rawURL := range candidates {
		norm := normalizeAlibabaBrowserImageURL(rawURL)
		if norm == "" {
			continue
		}
		if !validAlibabaImage(norm) {
			return fmt.Errorf("untrusted product image URL: %s", rawURL)
		}
		if _, dup := seen[norm]; dup {
			continue
		}
		seen[norm] = struct{}{}
		cleaned = append(cleaned, norm)
		if len(cleaned) >= 10 {
			break
		}
	}
	imgMap["images"] = cleaned
	data["image"] = imgMap
	return nil
}

func normalizeAlibabaBrowserImageURL(raw string) string {
	u := strings.TrimSpace(raw)
	if u == "" {
		return ""
	}
	if strings.HasPrefix(u, "//") {
		u = "https:" + u
	}
	if strings.HasPrefix(u, "img/ibank/") {
		if idx := strings.IndexAny(u, "?#"); idx >= 0 {
			u = u[:idx]
		}
		return thumbnailSizeSuffix.ReplaceAllString(u, ".")
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return u
	}
	if parsed.Scheme == "http" && strings.HasSuffix(parsed.Hostname(), ".alicdn.com") {
		parsed.Scheme = "https"
	}
	if strings.HasSuffix(parsed.Hostname(), ".alicdn.com") && strings.HasPrefix(parsed.Path, "/img/ibank/") {
		parsed.RawQuery = ""
		parsed.Fragment = ""
		parsed.Path = thumbnailSizeSuffix.ReplaceAllString(parsed.Path, ".")
		return parsed.String()
	}
	return u
}

func mergeBrowserProduct(existing, incoming Product) Product {
	merged := make(map[string]any, len(existing.Data)+len(incoming.Data))
	for k, v := range existing.Data {
		merged[k] = v
	}
	for k, v := range incoming.Data {
		if k == "description" {
			if desc, _ := v.(string); strings.TrimSpace(desc) == "" {
				if prevDesc, _ := existing.Data["description"].(string); strings.TrimSpace(prevDesc) != "" {
					continue
				}
			}
		}
		if k == "skuInfos" {
			if list, ok := v.([]any); ok && len(list) == 0 {
				if prevList, ok := existing.Data["skuInfos"].([]any); ok && len(prevList) > 0 {
					continue
				}
			}
		}
		if k == "image" {
			prevImgs := extractImageSlice(existing.Data["image"])
			nextImgs := extractImageSlice(v)
			if len(nextImgs) == 0 && len(prevImgs) > 0 {
				continue
			}
			if len(nextImgs) == 1 && len(prevImgs) > 1 && prevImgs[0] == nextImgs[0] {
				continue
			}
		}
		merged[k] = v
	}
	return Product{
		ID:       incoming.ID,
		Data:     merged,
		SyncedAt: incoming.SyncedAt,
	}
}

func extractImageSlice(raw any) []string {
	m, ok := raw.(map[string]any)
	if !ok || m == nil {
		return nil
	}
	b, err := json.Marshal(m["images"])
	if err != nil {
		return nil
	}
	var out []string
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}
