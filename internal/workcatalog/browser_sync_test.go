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
	if err != nil || queried.Total != 1 || !strings.HasSuffix(queried.Items[0].Thumbnail, "/img/ibank/item501a.jpg") {
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
