package workcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakePlatform struct {
	mu        sync.Mutex
	products  []Product
	live      Product
	listError bool
	writeErr  bool
	reads     int
	writes    int
}

func (f *fakePlatform) List(_ context.Context, page int, _, _ time.Time) (Page, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listError {
		return Page{}, errors.New("offline")
	}
	start := min((page-1)*20, len(f.products))
	return Page{Products: f.products[start:min(start+20, len(f.products))], Total: len(f.products)}, nil
}

func (f *fakePlatform) Get(context.Context, string) (Product, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return copyProduct(f.live), nil
}

func (f *fakePlatform) Update(_ context.Context, _ Product, patch Patch) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	if patch.Subject != nil {
		f.live.Data["subject"] = *patch.Subject
	}
	if patch.Description != nil {
		f.live.Data["description"] = *patch.Description
	}
	if patch.Images != nil {
		f.live.Data["image"] = map[string]any{"images": *patch.Images}
	}
	if f.writeErr {
		return errors.New("connection lost after write")
	}
	return nil
}

func copyProduct(p Product) Product {
	raw, _ := json.Marshal(p)
	var out Product
	_ = json.Unmarshal(raw, &out)
	return out
}

func product(id string) Product {
	return Product{ID: id, Data: map[string]any{"productID": id, "sellerLoginId": "factory-a", "subject": "Original title",
		"description": "Original details", "skuInfos": []any{map[string]any{"skuId": json.Number("9223372036854775806"), "amountOnSale": 12}},
		"image": map[string]any{"images": []string{"img/ibank/original.jpg"}}}, SyncedAt: time.Now().UTC()}
}

func setupCatalog(t *testing.T) (*Service, Scope, *fakePlatform, Draft) {
	t.Helper()
	s := NewService(NewStore(t.TempDir()))
	scope := Scope{StoreID: "store-a", SellerLoginID: "factory-a"}
	p := product("123")
	f := &fakePlatform{products: []Product{p}, live: copyProduct(p)}
	if _, err := s.Sync(context.Background(), scope, f, SyncInput{}); err != nil {
		t.Fatal(err)
	}
	title := "Improved factory wholesale title"
	draft, err := s.CreateDraft(context.Background(), scope, p.ID, Patch{Subject: &title})
	if err != nil {
		t.Fatal(err)
	}
	return s, scope, f, draft
}

func TestSyncResumesAfterFailureAndRestartWithoutExposingPartialSnapshot(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s := NewService(NewStore(root))
	scope := Scope{StoreID: "store-a", SellerLoginID: "factory-a"}
	f := &fakePlatform{}
	for i := range 21 {
		f.products = append(f.products, product(fmt.Sprint(i+1)))
	}
	first, err := s.Sync(ctx, scope, f, SyncInput{})
	if err != nil || first.Status != "running" || first.NextPage != 2 || first.Fetched != 20 {
		t.Fatal(first, err)
	}
	list, err := s.ListProducts(ctx, scope, 0, 100)
	if err != nil || list.Total != 0 {
		t.Fatal("partial snapshot was exposed", list, err)
	}
	f.listError = true
	if _, err := s.Sync(ctx, scope, f, SyncInput{}); err == nil {
		t.Fatal("offline page was accepted")
	}
	f.listError = false
	s = NewService(NewStore(root))
	last, err := s.Sync(ctx, scope, f, SyncInput{})
	if err != nil || last.ID != first.ID || last.Status != "completed" || last.Fetched != 21 {
		t.Fatal(last, err)
	}
	p, err := s.GetProduct(ctx, scope, "1")
	if err != nil {
		t.Fatal(err)
	}
	sku := p.Data["skuInfos"].([]any)[0].(map[string]any)["skuId"]
	if sku != json.Number("9223372036854775806") {
		t.Fatal("platform ID lost precision", sku)
	}
}

func TestSyncRejectsAnotherSellerAndDuplicatePages(t *testing.T) {
	for _, kind := range []string{"seller", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			s, scope, f, _ := setupCatalog(t)
			if kind == "seller" {
				f.products = []Product{product("456")}
				f.products[0].Data["sellerLoginId"] = "factory-b"
			} else {
				f.products = []Product{product("456"), product("456")}
			}
			if _, err := s.Sync(context.Background(), scope, f, SyncInput{Restart: true}); err == nil {
				t.Fatal("invalid platform page accepted")
			}
			list, _ := s.ListProducts(context.Background(), scope, 0, 20)
			if list.Total != 1 || list.Items[0].ID != "123" {
				t.Fatal("failed sync changed the old snapshot", list)
			}
		})
	}
}

func TestIncrementalSyncPreservesUnchangedProductsAndDrafts(t *testing.T) {
	s, scope, f, draft := setupCatalog(t)
	f.products = []Product{product("456")}
	result, err := s.Sync(context.Background(), scope, f, SyncInput{Since: time.Now().Add(-time.Hour).Format(time.RFC3339)})
	if err != nil || result.Status != "completed" {
		t.Fatal(result, err)
	}
	list, _ := s.ListProducts(context.Background(), scope, 0, 20)
	if list.Total != 2 {
		t.Fatal("incremental sync removed unchanged products", list)
	}
	if d, err := s.GetDraft(context.Background(), scope, draft.ID); err != nil || d.Revision != draft.Revision {
		t.Fatal(d, err)
	}
}

func TestPublicationApprovalConflictAndUnrelatedLiveStock(t *testing.T) {
	for _, kind := range []string{"denied", "revision", "conflict", "stock-only", "foreign-owner"} {
		t.Run(kind, func(t *testing.T) {
			s, scope, f, d := setupCatalog(t)
			authorize := func(context.Context, Draft) error { return nil }
			revision := d.Revision
			switch kind {
			case "denied":
				authorize = func(context.Context, Draft) error { return errors.New("rejected") }
			case "revision":
				revision = "stale"
			case "conflict":
				f.live.Data["subject"] = "Changed by another operator"
			case "stock-only":
				f.live.Data["saleInfo"] = map[string]any{"amountOnSale": 9}
			case "foreign-owner":
				f.live.Data["sellerLoginId"] = "factory-b"
			}
			r, err := s.Publish(context.Background(), scope, f, d.ID, revision, authorize)
			if kind == "stock-only" {
				if err != nil || r.Status != "published" || f.writes != 1 {
					t.Fatal(r, err, f.writes)
				}
			} else if f.writes != 0 || kind == "conflict" && (err != nil || r.Status != "conflict") || kind != "conflict" && err == nil {
				t.Fatal("unsafe publication", kind, r, err, f.writes)
			}
			if (kind == "denied" || kind == "revision") && f.reads != 0 {
				t.Fatal("unapproved publication reached the platform")
			}
		})
	}
}

func TestUncertainPublicationIsNeverReplayedAndCanBeVerifiedAfterRestart(t *testing.T) {
	s, scope, f, d := setupCatalog(t)
	f.writeErr = true
	allow := func(context.Context, Draft) error { return nil }
	r, err := s.Publish(context.Background(), scope, f, d.ID, d.Revision, allow)
	if err != nil || r.Status != "unverified" {
		t.Fatal(r, err)
	}
	s = NewService(NewStore(s.store.root))
	if _, err := s.Publish(context.Background(), scope, f, d.ID, d.Revision, allow); err == nil || f.writes != 1 {
		t.Fatal("uncertain write was repeated", err, f.writes)
	}
	r, err = s.Verify(context.Background(), scope, f, d.ID)
	if err != nil || r.Status != "published" || f.writes != 1 {
		t.Fatal(r, err, f.writes)
	}
	r, err = s.Publish(context.Background(), scope, f, d.ID, d.Revision, nil)
	if err != nil || r.Status != "published" || f.writes != 1 {
		t.Fatal("published draft was not idempotent", r, err)
	}
}

func TestConcurrentPublicationWritesOnceAcrossStoreInstances(t *testing.T) {
	s, scope, f, d := setupCatalog(t)
	other := NewService(NewStore(s.store.root))
	var wg sync.WaitGroup
	errorsCh := make(chan error, 2)
	for _, service := range []*Service{s, other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := service.Publish(context.Background(), scope, f, d.ID, d.Revision, func(context.Context, Draft) error { return nil })
			if err == nil && r.Status != "published" {
				err = fmt.Errorf("unexpected receipt: %+v", r)
			}
			errorsCh <- err
		}()
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	if f.writes != 1 {
		t.Fatal("duplicate online writes", f.writes)
	}
}

func TestCorruptCatalogIsBackedUpWithoutOverwritingAndOwnersCannotChange(t *testing.T) {
	s, scope, _, _ := setupCatalog(t)
	if _, err := s.store.load(context.Background(), Scope{StoreID: scope.StoreID, SellerLoginID: "other"}); err == nil {
		t.Fatal("catalog was rebound to another seller")
	}
	path, _ := s.store.path(scope)
	broken := []byte(`{"products":`)
	if err := os.WriteFile(path, broken, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.load(context.Background(), scope); err == nil {
		t.Fatal("corruption was ignored")
	}
	backups, _ := filepath.Glob(path + ".corrupt-*")
	if len(backups) != 1 {
		t.Fatal("missing corrupt backup", backups)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != string(broken) {
		t.Fatal("corrupt original was overwritten")
	}
}
