package workcatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Query struct {
	Query    string `json:"query,omitempty"`
	Status   string `json:"status,omitempty"`
	HasDraft bool   `json:"hasDraft,omitempty"`
	Offset   int    `json:"offset,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	Snapshot string `json:"snapshot,omitempty"`
}

type ProductSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Thumbnail string    `json:"thumbnail,omitempty"`
	Status    string    `json:"status"`
	Category  string    `json:"category,omitempty"`
	Prices    []any     `json:"prices"`
	Currency  string    `json:"currency"`
	Stock     any       `json:"stock"`
	SKUCount  int       `json:"skuCount"`
	HasDraft  bool      `json:"hasDraft"`
	SyncedAt  time.Time `json:"syncedAt"`
}

type QueryResult struct {
	Items      []ProductSummary `json:"items"`
	Total      int              `json:"total"`
	NextOffset int              `json:"nextOffset,omitempty"`
	Snapshot   string           `json:"snapshot"`
	Statuses   []string         `json:"statuses"`
}

type DraftSummary struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	ProductID string    `json:"productId"`
	Title     string    `json:"title"`
	Revision  string    `json:"revision"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
	Receipt   *Receipt  `json:"receipt,omitempty"`
}

func (s *Service) QueryProducts(ctx context.Context, scope Scope, query Query) (QueryResult, error) {
	if query.Limit == 0 {
		query.Limit = 20
	}
	if query.Offset < 0 || query.Limit < 1 || query.Limit > 100 || len(query.Query) > 256 {
		return QueryResult{}, errors.New("invalid product query or pagination")
	}
	st, err := s.store.load(ctx, scope)
	if err != nil {
		return QueryResult{}, err
	}
	ids := make([]string, 0, len(st.Products))
	for id := range st.Products {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	// Only fields affecting the list participate: opaque hash prevents paging a
	// newly synchronized snapshot with offsets from an older one.
	digest := sha256.New()
	for _, id := range ids {
		_, _ = fmt.Fprintf(digest, "%s:%s;", id, st.Products[id].SyncedAt.Format(time.RFC3339Nano))
	}
	hasDraft := map[string]bool{}
	draftIDs := make([]string, 0, len(st.Drafts))
	for id := range st.Drafts {
		draftIDs = append(draftIDs, id)
	}
	sort.Strings(draftIDs)
	for _, id := range draftIDs {
		d := st.Drafts[id]
		_, _ = fmt.Fprintf(digest, "%s:%s;", id, d.Status)
		if d.Status == "draft" || d.Status == "conflict" {
			hasDraft[d.ProductID] = true
		}
	}
	snapshot := hex.EncodeToString(digest.Sum(nil))
	if query.Snapshot != "" && query.Snapshot != snapshot {
		return QueryResult{}, errors.New("catalog_snapshot_changed")
	}
	statuses := map[string]bool{}
	items := []ProductSummary{}
	needle := strings.ToLower(strings.TrimSpace(query.Query))
	for _, id := range ids {
		p := st.Products[id]
		status, _ := p.Data["status"].(string)
		statuses[status] = status != ""
		if query.Status != "" && status != query.Status || query.HasDraft && !hasDraft[id] {
			continue
		}
		title, _ := p.Data["subject"].(string)
		if needle != "" {
			search := id + "\n" + title
			if sku, ok := p.Data["skuInfos"].([]any); ok {
				for _, raw := range sku {
					if item, ok := raw.(map[string]any); ok {
						for _, key := range []string{"skuId", "skuCode", "cargoNumber", "specId"} {
							search += "\n" + scalarID(item[key])
						}
					}
				}
			}
			if !strings.Contains(strings.ToLower(search), needle) {
				continue
			}
		}
		summary := ProductSummary{ID: id, Title: title, Status: status, HasDraft: hasDraft[id], SyncedAt: p.SyncedAt, Prices: []any{}, Currency: "CNY"}
		summary.Category, _ = p.Data["categoryName"].(string)
		if sale, ok := p.Data["saleInfo"].(map[string]any); ok {
			summary.Stock = sale["amountOnSale"]
			if prices, ok := sale["priceRanges"].([]any); ok {
				summary.Prices = prices
			}
		}
		if sku, ok := p.Data["skuInfos"].([]any); ok {
			summary.SKUCount = len(sku)
		}
		if image, ok := p.Data["image"].(map[string]any); ok {
			raw, _ := json.Marshal(image["images"])
			var images []string
			if json.Unmarshal(raw, &images) == nil && len(images) > 0 {
				if validAlibabaImage(images[0]) {
					summary.Thumbnail = images[0]
					if strings.HasPrefix(summary.Thumbnail, "img/") {
						summary.Thumbnail = "https://cbu01.alicdn.com/" + summary.Thumbnail
					}
				}
			}
		}
		items = append(items, summary)
	}
	out := QueryResult{Items: []ProductSummary{}, Total: len(items), Snapshot: snapshot, Statuses: []string{}}
	for status, valid := range statuses {
		if valid {
			out.Statuses = append(out.Statuses, status)
		}
	}
	sort.Strings(out.Statuses)
	if query.Offset >= len(items) {
		return out, nil
	}
	end := min(query.Offset+query.Limit, len(items))
	out.Items = items[query.Offset:end]
	if end < len(items) {
		out.NextOffset = end
	}
	return out, nil
}

func (s *Service) QueryDrafts(ctx context.Context, scope Scope) ([]DraftSummary, error) {
	st, err := s.store.load(ctx, scope)
	if err != nil {
		return nil, err
	}
	out := []DraftSummary{}
	for _, d := range st.Drafts {
		kind := d.Kind
		if kind == "" {
			kind = "update"
		}
		title, _ := d.Base.Data["subject"].(string)
		if d.NewProduct != nil {
			title = d.NewProduct.Subject
		}
		out = append(out, DraftSummary{ID: d.ID, Kind: kind, ProductID: d.ProductID, Title: title, Revision: d.Revision,
			Status: d.Status, CreatedAt: d.CreatedAt, Receipt: d.Receipt})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}
