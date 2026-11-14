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
	incomingIsDetail := hasNonEmptyCatalogString(incoming.Data["description"]) ||
		hasNonEmptyCatalogSlice(incoming.Data["attributes"])
	merged := make(map[string]any, len(existing.Data)+len(incoming.Data))
	for k, v := range existing.Data {
		merged[k] = v
	}
	for k, v := range incoming.Data {
		if v == nil {
			if existing.Data[k] != nil {
				continue
			}
		}
		if k == "description" || k == "categoryName" || k == "sourceUrl" {
			if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
				if hasNonEmptyCatalogString(existing.Data[k]) {
					continue
				}
			}
		}
		if k == "attributes" {
			if !hasNonEmptyCatalogSlice(v) && hasNonEmptyCatalogSlice(existing.Data["attributes"]) {
				continue
			}
		}
		if k == "shippingInfo" {
			if prevMap, ok := existing.Data["shippingInfo"].(map[string]any); ok && len(prevMap) > 0 {
				if nextMap, nextOK := v.(map[string]any); !nextOK || len(nextMap) == 0 {
					continue
				}
			}
		}
		if k == "skuInfos" {
			if shouldPreserveExistingSKUs(existing.Data["skuInfos"], v, incomingIsDetail) {
				continue
			}
		}
		if k == "saleInfo" {
			merged["saleInfo"] = mergeBrowserSaleInfo(existing.Data["saleInfo"], v, incomingIsDetail)
			continue
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
			if !incomingIsDetail && len(nextImgs) < len(prevImgs) && stringSliceSubset(nextImgs, prevImgs) {
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

func hasNonEmptyCatalogString(raw any) bool {
	s, ok := raw.(string)
	return ok && strings.TrimSpace(s) != ""
}

func hasNonEmptyCatalogSlice(raw any) bool {
	s, ok := raw.([]any)
	return ok && len(s) > 0
}

func shouldPreserveExistingSKUs(prevRaw, nextRaw any, incomingIsDetail bool) bool {
	prevList, ok := prevRaw.([]any)
	if !ok || len(prevList) == 0 {
		return false
	}
	nextList, ok := nextRaw.([]any)
	if !ok || len(nextList) == 0 {
		return true
	}
	if isSummarySKUList(nextList) && (len(prevList) > 1 || !isSummarySKUList(prevList)) {
		return true
	}
	if !incomingIsDetail && len(nextList) < len(prevList) {
		return true
	}
	return false
}

// isSummarySKUList detects the single-item placeholder SKU (`skuId: "1"` with
// only `cargoNumber`) emitted when extracting a seller backend list row.
func isSummarySKUList(list []any) bool {
	if len(list) != 1 {
		return false
	}
	item, ok := list[0].(map[string]any)
	if !ok || item == nil {
		return false
	}
	return scalarID(item["skuId"]) == "1" &&
		scalarID(item["specId"]) == "" &&
		scalarID(item["skuCode"]) == "" &&
		item["price"] == nil &&
		item["amountOnSale"] == nil
}

func mergeBrowserSaleInfo(prevRaw, nextRaw any, incomingIsDetail bool) any {
	prevMap, prevOK := prevRaw.(map[string]any)
	nextMap, nextOK := nextRaw.(map[string]any)
	if !nextOK || nextMap == nil {
		if prevOK && prevMap != nil {
			return prevMap
		}
		return nextRaw
	}
	if !prevOK || prevMap == nil {
		return nextMap
	}
	out := make(map[string]any, len(prevMap)+len(nextMap))
	for k, v := range prevMap {
		out[k] = v
	}
	for k, v := range nextMap {
		if v == nil {
			if prevMap[k] != nil {
				continue
			}
		}
		if k == "amountOnSale" {
			if s, ok := v.(string); ok && strings.TrimSpace(s) == "" && prevMap[k] != nil {
				continue
			}
		}
		if k == "priceRanges" && shouldPreservePriceRanges(prevMap["priceRanges"], v, incomingIsDetail) {
			continue
		}
		out[k] = v
	}
	return out
}

func shouldPreservePriceRanges(prevRaw, nextRaw any, incomingIsDetail bool) bool {
	prevRanges, ok := prevRaw.([]any)
	if !ok || len(prevRanges) == 0 {
		return false
	}
	nextRanges, ok := nextRaw.([]any)
	if !ok || len(nextRanges) == 0 {
		return true
	}
	if incomingIsDetail || !priceRangesSubset(nextRanges, prevRanges) {
		return false
	}
	if len(nextRanges) < len(prevRanges) {
		return true
	}
	if len(nextRanges) == 1 && len(prevRanges) == 1 {
		nextStart, nextOK := rangeFieldFloat(nextRanges[0], "startQuantity")
		prevStart, prevOK := rangeFieldFloat(prevRanges[0], "startQuantity")
		if nextOK && prevOK && nextStart == 1 && prevStart > 1 {
			return true
		}
	}
	return false
}

func priceRangesSubset(subset, superset []any) bool {
	known := make(map[float64]struct{}, len(superset))
	for _, raw := range superset {
		if p, ok := rangeFieldFloat(raw, "price"); ok {
			known[p] = struct{}{}
		}
	}
	if len(known) == 0 {
		return false
	}
	for _, raw := range subset {
		p, ok := rangeFieldFloat(raw, "price")
		if !ok {
			return false
		}
		if _, exists := known[p]; !exists {
			return false
		}
	}
	return true
}

func rangeFieldFloat(raw any, key string) (float64, bool) {
	m, ok := raw.(map[string]any)
	if !ok || m == nil {
		return 0, false
	}
	switch n := m[key].(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case string:
		f, err := json.Number(strings.TrimSpace(n)).Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func stringSliceSubset(subset, superset []string) bool {
	seen := make(map[string]struct{}, len(superset))
	for _, s := range superset {
		seen[s] = struct{}{}
	}
	for _, s := range subset {
		if _, ok := seen[s]; !ok {
			return false
		}
	}
	return true
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
