package workcatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserSyncBindsSellerMergesDetailsAndUpgradesToOpenAPISeamlessly(t *testing.T) {
	ctx := context.Background()
	svc := NewService(NewStore(t.TempDir()))
	t.Cleanup(svc.Close)

	settings := StoreSettings{StoreID: "store-browser-1", Platform: "1688"}
	initialTarget, err := svc.ResolveTarget(ctx, settings)
	if err != nil {
		t.Fatal(err)
	}
	if initialTarget.Authorization.Configured || initialTarget.Scope.SellerLoginID != "unconfigured" {
		t.Fatalf("expected unconfigured initial target, got %+v", initialTarget)
	}

	firstSync, err := svc.SyncBrowserProducts(ctx, initialTarget, BrowserSyncInput{
		SellerLoginID: "factory-a",
		Products: []BrowserProductInput{
			{
				ID: "501",
				Data: map[string]any{
					"subject":      "Original braided cable",
					"status":       "published",
					"categoryName": "Cables",
					"description":  "<p>Factory details</p>",
					"saleInfo": map[string]any{
						"amountOnSale": 500,
						"priceRanges": []any{
							map[string]any{"startQuantity": 2, "price": 9.8},
						},
					},
					"skuInfos": []any{
						map[string]any{"skuId": "701", "cargoNumber": "SKU-501"},
					},
					"image": map[string]any{
						"images": []string{
							"img/ibank/item501a.220x220.jpg?v=1",
							"img/ibank/item501b.jpg",
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("first browser sync failed: %v", err)
	}
	if firstSync.Authorization.Source != "browser" || !firstSync.Authorization.Configured || firstSync.ProductCount != 1 {
		t.Fatalf("unexpected browser sync result: %+v", firstSync)
	}

	browserTarget, err := svc.ResolveTarget(ctx, settings)
	if err != nil {
		t.Fatal(err)
	}
	if browserTarget.Authorization.Source != "browser" || browserTarget.Capabilities.Sync || browserTarget.Capabilities.PublishUpdate {
		t.Fatalf("expected browser target to disable background API sync and direct publish, got %+v", browserTarget)
	}
	if !browserTarget.Capabilities.Read || !browserTarget.Capabilities.EditDraft || !browserTarget.Capabilities.NewDraft {
		t.Fatalf("expected browser target to keep read/edit/newDraft capabilities, got %+v", browserTarget.Capabilities)
	}

	secondSync, err := svc.SyncBrowserProducts(ctx, browserTarget, BrowserSyncInput{
		Products: []BrowserProductInput{
			{
				ID: "501",
				Data: map[string]any{
					"subject":     "Updated braided cable",
					"status":      "published",
					"description": "",
					"skuInfos":    []any{},
					"image": map[string]any{
						"images": []string{"img/ibank/item501a.jpg"},
					},
				},
			},
		},
	})
	if err != nil || secondSync.ProductCount != 1 {
		t.Fatalf("incremental summary refresh failed: %v (%+v)", err, secondSync)
	}
	stored, err := svc.GetProduct(ctx, browserTarget.Scope, "501")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Data["subject"] != "Updated braided cable" {
		t.Fatalf("expected updated subject, got %v", stored.Data["subject"])
	}
	if stored.Data["description"] != "<p>Factory details</p>" {
		t.Fatalf("expected existing description to be preserved, got %v", stored.Data["description"])
	}
	if len(extractImageSlice(stored.Data["image"])) != 2 {
		t.Fatalf("expected enriched 2-image gallery to be preserved, got %v", stored.Data["image"])
	}

	newSubj := "Wholesale braided Type-C cable"
	draft, err := svc.CreateDraftForPlatform(ctx, "1688", browserTarget.Scope, "501", Patch{Subject: &newSubj})
	if err != nil || draft.ID == "" {
		t.Fatalf("CreateDraft on browser-synced product failed: %v", err)
	}
	queried, err := svc.QueryProducts(ctx, browserTarget.Scope, Query{Query: "SKU-501", HasDraft: true})
	if err != nil || queried.Total != 1 || !queried.Items[0].HasDetail || !strings.HasSuffix(queried.Items[0].Thumbnail, "/img/ibank/item501a.jpg") {
		t.Fatalf("QueryProducts failed on browser-synced snapshot: %+v (%v)", queried, err)
	}

	if _, err := svc.SyncBrowserProducts(ctx, browserTarget, BrowserSyncInput{
		SellerLoginID: "factory-b",
		Products: []BrowserProductInput{
			{ID: "502", Data: map[string]any{"subject": "Foreign item"}},
		},
	}); err == nil {
		t.Fatal("expected foreign sellerLoginId in browser sync to be rejected")
	}
	if _, err := svc.SyncBrowserProducts(ctx, browserTarget, BrowserSyncInput{
		Products: []BrowserProductInput{
			{
				ID: "503",
				Data: map[string]any{
					"subject": "Invalid image path item",
					"image":   map[string]any{"images": []string{"img/ibank/../outside.jpg"}},
				},
			},
		},
	}); err == nil {
		t.Fatal("expected invalid image-bank path to be rejected")
	}

	openAuth, err := svc.ConfigureAuthorization(ctx, settings.StoreID, testCredentials())
	if err != nil || openAuth.Source != "local" {
		t.Fatalf("upgrade to OpenAPI credentials failed: %v (%+v)", err, openAuth)
	}
	upgradedTarget, err := svc.ResolveTarget(ctx, settings)
	if err != nil {
		t.Fatal(err)
	}
	if !upgradedTarget.Capabilities.Sync || !upgradedTarget.Capabilities.PublishUpdate {
		t.Fatalf("expected OpenAPI upgrade to re-enable Sync and PublishUpdate, got %+v", upgradedTarget.Capabilities)
	}
	if preservedDraft, err := svc.GetDraft(ctx, upgradedTarget.Scope, draft.ID); err != nil || preservedDraft.Revision != draft.Revision {
		t.Fatalf("expected draft to survive OpenAPI upgrade, got %+v (%v)", preservedDraft, err)
	}
	afterUpgradeSync, err := svc.SyncBrowserProducts(ctx, upgradedTarget, BrowserSyncInput{
		SellerLoginID: "factory-a",
		Products: []BrowserProductInput{
			{ID: "504", Data: map[string]any{"subject": "Additional cable model"}},
		},
	})
	if err != nil || afterUpgradeSync.Authorization.Source != "local" || afterUpgradeSync.ProductCount != 2 {
		t.Fatalf("browser sync after OpenAPI upgrade altered auth source or count: %+v (%v)", afterUpgradeSync, err)
	}
}

func TestHTTPFacadeBrowserAuthAndSyncEndpoints(t *testing.T) {
	ctx := context.Background()
	svc := NewService(NewStore(t.TempDir()))
	t.Cleanup(svc.Close)
	settings := StoreSettings{StoreID: "store-http-browser", Platform: "1688"}

	handler := NewHTTPHandler(
		svc,
		func(*http.Request) (Target, error) {
			return svc.ResolveTarget(ctx, settings)
		},
		func(*http.Request) (Authorize, error) {
			return func(context.Context, Draft) error { return nil }, nil
		},
	)

	req := httptest.NewRequest(http.MethodPut, "/api/hoxi/work/catalog/browser-auth?storeId=store-http-browser",
		strings.NewReader(`{"sellerLoginId":"factory-a"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"source":"browser"`) {
		t.Fatalf("browser-auth failed: %d %s", rec.Code, rec.Body.String())
	}

	syncReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/work/catalog/browser-sync?storeId=store-http-browser",
		strings.NewReader(`{"products":[{"id":"505","data":{"subject":"Direct factory cable","status":"published"}}]}`))
	syncReq.Header.Set("Content-Type", "application/json")
	syncRec := httptest.NewRecorder()
	handler.ServeHTTP(syncRec, syncReq)
	if syncRec.Code != http.StatusOK || !strings.Contains(syncRec.Body.String(), `"productCount":1`) {
		t.Fatalf("browser-sync failed: %d %s", syncRec.Code, syncRec.Body.String())
	}
}

func TestBrowserListSyncPreservesEnrichedSKUsStockAndTierPrices(t *testing.T) {
	ctx := context.Background()
	svc := NewService(NewStore(t.TempDir()))
	t.Cleanup(svc.Close)

	settings := StoreSettings{StoreID: "store-merge-guard", Platform: "1688"}
	initial, err := svc.ResolveTarget(ctx, settings)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Seed a detail-enriched product with 2 full SKUs, known stock, 2 tier prices,
	// attributes, categoryName, sourceUrl, and a 3-image gallery.
	_, err = svc.SyncBrowserProducts(ctx, initial, BrowserSyncInput{
		SellerLoginID: "factory-merge",
		Products: []BrowserProductInput{
			{
				ID: "601",
				Data: map[string]any{
					"subject":      "Enriched CNC aluminum stand",
					"status":       "published",
					"categoryName": "Laptop Stands",
					"sourceUrl":    "https://detail.1688.com/offer/601.html",
					"description":  "<ul><li><strong>材质：</strong>铝合金</li></ul><p>Detail body</p>",
					"attributes": []any{
						map[string]any{"attributeName": "材质", "value": "铝合金"},
					},
					"saleInfo": map[string]any{
						"amountOnSale": 850,
						"priceRanges": []any{
							map[string]any{"startQuantity": 2, "price": 39.0},
							map[string]any{"startQuantity": 50, "price": 32.5},
						},
					},
					"skuInfos": []any{
						map[string]any{"skuId": "9001", "specId": "s1", "cargoNumber": "STD-SLV", "price": "39.0", "amountOnSale": 500},
						map[string]any{"skuId": "9002", "specId": "s2", "cargoNumber": "STD-GRY", "price": "39.0", "amountOnSale": 350},
					},
					"image": map[string]any{
						"images": []string{
							"img/ibank/stand1.jpg",
							"img/ibank/stand2.jpg",
							"img/ibank/stand3.jpg",
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("seed enriched product failed: %v", err)
	}

	target, err := svc.ResolveTarget(ctx, settings)
	if err != nil {
		t.Fatal(err)
	}

	// 2. Simulate Mode B seller-list sync: 1 synthetic summary SKU (`skuId: "1"`),
	// null stock, empty tier prices, and a single row thumbnail.
	_, err = svc.SyncBrowserProducts(ctx, target, BrowserSyncInput{
		Products: []BrowserProductInput{
			{
				ID: "601",
				Data: map[string]any{
					"subject": "Updated CNC aluminum stand",
					"status":  "published",
					"saleInfo": map[string]any{
						"amountOnSale": nil,
						"priceRanges":  []any{},
					},
					"skuInfos": []any{
						map[string]any{"skuId": "1", "cargoNumber": "STD-SLV"},
					},
					"image": map[string]any{
						"images": []string{"img/ibank/stand1.jpg"},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("list summary sync failed: %v", err)
	}

	queried, err := svc.QueryProducts(ctx, target.Scope, Query{Query: "STD-GRY"})
	if err != nil {
		t.Fatal(err)
	}
	if queried.Total != 1 {
		t.Fatalf("expected second full SKU cargoNumber to remain searchable, got %+v", queried)
	}
	item := queried.Items[0]
	if item.Title != "Updated CNC aluminum stand" {
		t.Fatalf("expected updated list title, got %q", item.Title)
	}
	if item.SKUCount != 2 {
		t.Fatalf("expected 2 full SKUs to survive summary list sync, got %d", item.SKUCount)
	}
	if scalarID(item.Stock) != "850" {
		t.Fatalf("expected stock 850 to survive null list stock, got %v", item.Stock)
	}
	if len(item.Prices) != 2 {
		t.Fatalf("expected 2 tier prices to survive empty list priceRanges, got %v", item.Prices)
	}
	if !item.HasDetail || item.Category != "Laptop Stands" {
		t.Fatalf("expected HasDetail and category to survive list sync, got %+v", item)
	}

	// 3. When a subsequent list sync reports a genuinely changed price and zero stock,
	// those real updates must take effect while still preserving the 2 full SKUs.
	_, err = svc.SyncBrowserProducts(ctx, target, BrowserSyncInput{
		Products: []BrowserProductInput{
			{
				ID: "601",
				Data: map[string]any{
					"subject": "Updated CNC aluminum stand",
					"status":  "expired",
					"saleInfo": map[string]any{
						"amountOnSale": 0,
						"priceRanges": []any{
							map[string]any{"startQuantity": 1, "price": 41.0},
						},
					},
					"skuInfos": []any{
						map[string]any{"skuId": "1", "cargoNumber": "STD-SLV"},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("list price/stock update sync failed: %v", err)
	}
	updatedQuery, err := svc.QueryProducts(ctx, target.Scope, Query{})
	if err != nil || updatedQuery.Total != 1 {
		t.Fatalf("query after list price update failed: %+v (%v)", updatedQuery, err)
	}
	updated := updatedQuery.Items[0]
	if updated.Status != "expired" || scalarID(updated.Stock) != "0" || len(updated.Prices) != 1 || updated.SKUCount != 2 {
		t.Fatalf("expected explicit status/zero-stock/new-price update while keeping 2 SKUs, got %+v", updated)
	}
}

func TestBrowserListSyncPreservesMultiTierPriceThresholdsAndRejectsMismatchedItemSeller(t *testing.T) {
	ctx := context.Background()
	svc := NewService(NewStore(t.TempDir()))
	t.Cleanup(svc.Close)

	settings := StoreSettings{StoreID: "store-tier-moq-guard", Platform: "1688"}
	initial, err := svc.ResolveTarget(ctx, settings)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Seed detail-enriched product whose real MOQ thresholds are 3 / 50.
	_, err = svc.SyncBrowserProducts(ctx, initial, BrowserSyncInput{
		SellerLoginID: "factory-a",
		Products: []BrowserProductInput{
			{
				ID: "701",
				Data: map[string]any{
					"subject":     "Industrial braided hydraulic hose",
					"status":      "published",
					"description": "<p>High pressure hose detail</p>",
					"saleInfo": map[string]any{
						"amountOnSale": 1200,
						"priceRanges": []any{
							map[string]any{"startQuantity": 3, "price": 18.5},
							map[string]any{"startQuantity": 50, "price": 14.0},
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("seed detail product failed: %v", err)
	}

	target, err := svc.ResolveTarget(ctx, settings)
	if err != nil {
		t.Fatal(err)
	}

	// 2. Simulate list summary refresh with identical unit-price set {14.0, 18.5} and
	// same tier count (2), but synthetic list thresholds `1 / 10`.
	_, err = svc.SyncBrowserProducts(ctx, target, BrowserSyncInput{
		SellerLoginID: "factory-a",
		Products: []BrowserProductInput{
			{
				ID: "701",
				Data: map[string]any{
					"subject": "Industrial braided hydraulic hose",
					"status":  "published",
					"saleInfo": map[string]any{
						"amountOnSale": nil,
						"priceRanges": []any{
							map[string]any{"startQuantity": 1, "price": 14.0},
							map[string]any{"startQuantity": 10, "price": 18.5},
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("list summary refresh failed: %v", err)
	}

	stored, err := svc.GetProduct(ctx, target.Scope, "701")
	if err != nil {
		t.Fatal(err)
	}
	saleInfo, _ := stored.Data["saleInfo"].(map[string]any)
	ranges, _ := saleInfo["priceRanges"].([]any)
	if len(ranges) != 2 {
		t.Fatalf("expected 2 tier prices, got %#v", ranges)
	}
	firstStart, _ := rangeFieldFloat(ranges[0], "startQuantity")
	firstPrice, _ := rangeFieldFloat(ranges[0], "price")
	secondStart, _ := rangeFieldFloat(ranges[1], "startQuantity")
	secondPrice, _ := rangeFieldFloat(ranges[1], "price")
	if firstStart != 3 || firstPrice != 18.5 || secondStart != 50 || secondPrice != 14.0 {
		t.Fatalf("expected real MOQ thresholds 3@18.5 / 50@14.0 to survive list summary 1/10, got %#v", ranges)
	}

	// 3. Reject foreign seller items even when top-level SellerLoginID is "factory-a".
	for _, foreign := range []map[string]any{
		{"subject": "Foreign via sellerLoginId", "sellerLoginId": "factory-b"},
		{"subject": "Foreign via supplierLoginId", "sellerLoginId": "factory-a", "supplierLoginId": "factory-b"},
		{"subject": "Foreign via sourceUrl", "sourceUrl": "https://factory-b.1688.com/offer/702.html"},
	} {
		if _, err := svc.SyncBrowserProducts(ctx, target, BrowserSyncInput{
			SellerLoginID: "factory-a",
			Products:      []BrowserProductInput{{ID: "702", Data: foreign}},
		}); err == nil {
			t.Fatalf("expected foreign item %+v to be rejected", foreign)
		}
	}
}
