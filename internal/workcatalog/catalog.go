// Package workcatalog manages seller-owned product snapshots and immutable
// editing drafts. Platform writes are field-level and always followed by readback.
package workcatalog

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var productIDPattern = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)

type Product struct {
	ID       string         `json:"id"`
	Data     map[string]any `json:"data"`
	SyncedAt time.Time      `json:"syncedAt"`
}

// Patch deliberately matches 1688's incrementModify contract. Unsupported
// fields (including live SKU stock/prices) are rejected by the MCP input schema.
type Patch struct {
	Subject     *string   `json:"subject,omitempty" jsonschema:"Replacement product title"`
	Description *string   `json:"description,omitempty" jsonschema:"Replacement HTML product description"`
	Images      *[]string `json:"images,omitempty" jsonschema:"Replacement main gallery; one to five existing 1688 image URLs or image-bank paths"`
}

type Change struct {
	Field  string `json:"field"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

type Draft struct {
	ID         string           `json:"id"`
	Kind       string           `json:"kind,omitempty"`
	ProductID  string           `json:"productId"`
	Revision   string           `json:"revision"`
	Base       Product          `json:"base"`
	Patch      Patch            `json:"patch"`
	Changes    []Change         `json:"changes"`
	Status     string           `json:"status"`
	CreatedAt  time.Time        `json:"createdAt"`
	Receipt    *Receipt         `json:"receipt,omitempty"`
	NewProduct *NewProductInput `json:"newProduct,omitempty"`
}

type Receipt struct {
	DraftID   string    `json:"draftId"`
	ProductID string    `json:"productId"`
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
}

type Page struct {
	Products []Product
	Total    int
}

type Platform interface {
	List(context.Context, int, time.Time, time.Time) (Page, error)
	Get(context.Context, string) (Product, error)
	Update(context.Context, Product, Patch) error
}

type Scope struct {
	StoreID       string
	SellerLoginID string
}

// Authorize must use Hoxi's desktop permission plane, never a model-supplied bool.
type Authorize func(context.Context, Draft) error

type Service struct {
	store      *Store
	adaptersMu sync.RWMutex
	adapters   map[string]AdapterSpec
	jobsMu     sync.Mutex
	jobs       map[string]*syncWorker
	closed     bool
}

func NewService(store *Store) *Service {
	if store == nil {
		store = NewStore("")
	}
	svc := &Service{store: store, adapters: map[string]AdapterSpec{}, jobs: map[string]*syncWorker{}}
	svc.RegisterAdapter(alibaba1688AdapterSpec())
	return svc
}

func (s *Service) CreateDraft(ctx context.Context, scope Scope, productID string, patch Patch) (Draft, error) {
	return s.CreateDraftForPlatform(ctx, "1688", scope, productID, patch)
}

func (s *Service) CreateDraftForPlatform(ctx context.Context, platform string, scope Scope, productID string, patch Patch) (Draft, error) {
	validator := validatePatch
	if spec, ok := s.Adapter(platform); ok && spec.ValidatePatch != nil {
		validator = spec.ValidatePatch
	}
	if err := validator(patch); err != nil {
		return Draft{}, err
	}
	var draft Draft
	err := s.store.update(ctx, scope, func(st *state) error {
		base, ok := st.Products[productID]
		if !ok {
			return errors.New("product is not in the local snapshot; sync the store first")
		}
		if len(st.Drafts) >= 1000 {
			return errors.New("store draft limit reached (1000)")
		}
		changes := diff(base, patch)
		if len(changes) == 0 {
			return errors.New("draft has no changes")
		}
		id, err := newID("drf_")
		if err != nil {
			return err
		}
		draft = Draft{ID: id, Kind: "update", ProductID: productID, Base: base, Patch: patch,
			Changes: changes, Status: "draft", CreatedAt: time.Now().UTC()}
		raw, err := json.Marshal(draft)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		draft.Revision = hex.EncodeToString(sum[:])
		st.Drafts[draft.ID] = draft
		return nil
	})
	return draft, err
}

func (s *Service) GetDraft(ctx context.Context, scope Scope, id string) (Draft, error) {
	st, err := s.store.load(ctx, scope)
	if err != nil {
		return Draft{}, err
	}
	draft, ok := st.Drafts[id]
	if !ok {
		return Draft{}, errors.New("draft not found in this store")
	}
	return draft, nil
}

func (s *Service) DiscardDraft(ctx context.Context, scope Scope, id, revision string) (Draft, error) {
	var result Draft
	err := s.store.update(ctx, scope, func(st *state) error {
		d, ok := st.Drafts[id]
		if !ok || revision == "" || d.Revision != revision {
			return errors.New("draft not found or revision mismatch")
		}
		if d.Status != "draft" && d.Status != "conflict" && d.Status != "discarded" {
			return errors.New("a publication receipt cannot be discarded")
		}
		d.Status = "discarded"
		st.Drafts[id], result = d, d
		return nil
	})
	return result, err
}

type ProductList struct {
	Items      []Product `json:"items"`
	Total      int       `json:"total"`
	NextOffset int       `json:"nextOffset,omitempty"`
}

func (s *Service) ListProducts(ctx context.Context, scope Scope, offset, limit int) (ProductList, error) {
	if offset < 0 || limit < 1 || limit > 100 {
		return ProductList{}, errors.New("offset must be nonnegative and limit must be 1..100")
	}
	st, err := s.store.load(ctx, scope)
	if err != nil {
		return ProductList{}, err
	}
	ids := make([]string, 0, len(st.Products))
	for id := range st.Products {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := ProductList{Items: []Product{}, Total: len(ids)}
	if offset >= len(ids) {
		return result, nil
	}
	end := min(offset+limit, len(ids))
	for _, id := range ids[offset:end] {
		result.Items = append(result.Items, st.Products[id])
	}
	if end < len(ids) {
		result.NextOffset = end
	}
	return result, nil
}

func (s *Service) GetProduct(ctx context.Context, scope Scope, id string) (Product, error) {
	st, err := s.store.load(ctx, scope)
	if err != nil {
		return Product{}, err
	}
	product, ok := st.Products[id]
	if !ok {
		return Product{}, errors.New("product not found in this store snapshot")
	}
	return product, nil
}

func validatePatch(p Patch) error {
	if p.Subject == nil && p.Description == nil && p.Images == nil {
		return errors.New("at least one editable field is required")
	}
	if p.Subject != nil {
		if strings.TrimSpace(*p.Subject) == "" {
			return errors.New("subject must not be empty")
		}
		units := 0
		for _, char := range *p.Subject {
			units++
			if char > 127 {
				units++
			}
		}
		if units > 60 {
			return errors.New("subject exceeds 60 weighted units (30 Chinese characters)")
		}
	}
	if p.Description != nil && (strings.TrimSpace(*p.Description) == "" || len(*p.Description) > 100000) {
		return errors.New("description must contain 1..100000 bytes")
	}
	if p.Images != nil {
		if len(*p.Images) < 1 || len(*p.Images) > 5 {
			return errors.New("1688 main gallery must contain 1..5 images")
		}
		for _, image := range *p.Images {
			if !validAlibabaImage(image) {
				return errors.New("images must be existing 1688 image-bank paths or HTTPS alicdn.com URLs")
			}
		}
	}
	return nil
}

func diff(base Product, patch Patch) []Change {
	changes := []Change{}
	add := func(field string, after any) {
		before := fieldValue(base, field)
		if !equalField(field, before, after) {
			changes = append(changes, Change{Field: field, Before: before, After: after})
		}
	}
	if patch.Subject != nil {
		add("subject", *patch.Subject)
	}
	if patch.Description != nil {
		add("description", *patch.Description)
	}
	if patch.Images != nil {
		add("images", *patch.Images)
	}
	return changes
}

func fieldValue(p Product, field string) any {
	if field == "images" {
		image, _ := p.Data["image"].(map[string]any)
		return image["images"]
	}
	return p.Data[field]
}

func equalValue(a, b any) bool {
	x, e1 := json.Marshal(a)
	y, e2 := json.Marshal(b)
	return e1 == nil && e2 == nil && string(x) == string(y)
}

func equalField(field string, a, b any) bool {
	if field != "images" {
		return equalValue(a, b)
	}
	normalize := func(value any) any {
		raw, err := json.Marshal(value)
		if err != nil {
			return value
		}
		var images []string
		if json.Unmarshal(raw, &images) != nil {
			return value
		}
		for i, image := range images {
			if u, err := url.Parse(image); err == nil && strings.HasSuffix(u.Hostname(), ".alicdn.com") && strings.HasPrefix(u.Path, "/img/ibank/") && u.RawQuery == "" {
				images[i] = strings.TrimPrefix(u.Path, "/")
			}
		}
		return images
	}
	return equalValue(normalize(a), normalize(b))
}

func newID(prefix string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", errors.New("cannot allocate a catalog record ID")
	}
	return prefix + hex.EncodeToString(b[:]), nil
}
