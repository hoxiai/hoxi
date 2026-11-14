package workcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testCredentials() Credentials {
	return Credentials{SellerLoginID: "factory-a", AppKey: "123456", AppSecret: "private-app-secret", AccessToken: "private-seller-token"}
}

func TestAuthorizationPersistsPrivatelyAndRejectsSilentSellerRelink(t *testing.T) {
	s, scope, _, _ := setupCatalog(t)
	view, err := s.ConfigureAuthorization(context.Background(), scope.StoreID, testCredentials())
	if err != nil || !view.Configured || view.VerifiedRead {
		t.Fatal(view, err)
	}
	raw, _ := json.Marshal(view)
	if strings.Contains(string(raw), "private-app-secret") || strings.Contains(string(raw), "private-seller-token") {
		t.Fatal("authorization view exposed secrets")
	}
	path, _ := s.store.path(scope)
	info, err := os.Stat(path + ".auth.json")
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unsafe credential file permissions", info, err)
	}
	rotated := testCredentials()
	rotated.AccessToken, rotated.AppSecret = "rotated-seller-token", ""
	if _, err := s.ConfigureAuthorization(context.Background(), scope.StoreID, rotated); err != nil {
		t.Fatal(err)
	}
	target, err := s.ResolveTarget(context.Background(), StoreSettings{StoreID: scope.StoreID, Platform: "1688"})
	if err != nil || target.Scope.SellerLoginID != "factory-a" || target.Authorization.Source != "local" {
		t.Fatal(target.Authorization, err)
	}
	api, err := target.Platform()
	if err != nil || api.(*Alibaba1688).appSecret != "private-app-secret" || api.(*Alibaba1688).accessToken != "rotated-seller-token" {
		t.Fatal("credential rotation lost preserved values", err)
	}
	wrong := testCredentials()
	wrong.SellerLoginID = "factory-b"
	if _, err := s.ConfigureAuthorization(context.Background(), scope.StoreID, wrong); err == nil {
		t.Fatal("existing catalog was silently relinked")
	}
	if _, err := s.ConfigureAuthorization(context.Background(), scope.StoreID, Credentials{SellerLoginID: "factory-b", AppKey: "987654"}); err == nil {
		t.Fatal("secrets reused across another seller/application")
	}
}

func TestAuthorizationReadVerificationIsDurableAndRejectsStaleCredentials(t *testing.T) {
	s, scope, f, _ := setupCatalog(t)
	if _, err := s.ConfigureAuthorization(context.Background(), scope.StoreID, testCredentials()); err != nil {
		t.Fatal(err)
	}
	settings := StoreSettings{StoreID: scope.StoreID, Platform: "1688"}
	target, err := s.ResolveTarget(context.Background(), settings)
	if err != nil {
		t.Fatal(err)
	}
	target.Platform = func() (Platform, error) { return f, nil }
	view, err := s.CheckAuthorization(context.Background(), target)
	if err != nil || !view.VerifiedRead {
		t.Fatal(view, err)
	}
	fresh, err := s.ResolveTarget(context.Background(), settings)
	if err != nil || !fresh.Authorization.VerifiedRead || fresh.Capabilities.PublishNew {
		t.Fatal(fresh.Authorization, err)
	}
	rotated := testCredentials()
	rotated.AccessToken = "another-token"
	if _, err := s.ConfigureAuthorization(context.Background(), scope.StoreID, rotated); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CheckAuthorization(context.Background(), target); err == nil {
		t.Fatal("old credential response marked new credentials verified")
	}
	fresh, _ = s.ResolveTarget(context.Background(), settings)
	if fresh.Authorization.VerifiedRead {
		t.Fatal("stale read verification survived token rotation")
	}
}

func TestAuthorizationCorruptionIsBackedUpAndPreserved(t *testing.T) {
	s, scope, _, _ := setupCatalog(t)
	if _, err := s.ConfigureAuthorization(context.Background(), scope.StoreID, testCredentials()); err != nil {
		t.Fatal(err)
	}
	path, _ := s.store.path(scope)
	path += ".auth.json"
	broken := []byte(`{"credentials":`)
	if err := os.WriteFile(path, broken, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveTarget(context.Background(), StoreSettings{StoreID: scope.StoreID, Platform: "1688"}); err == nil {
		t.Fatal("invalid authorization was silently reset")
	}
	backups, _ := filepath.Glob(path + ".corrupt-*")
	if len(backups) != 1 {
		t.Fatal("missing corruption backup")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != string(broken) {
		t.Fatal("corrupt original was overwritten")
	}
}

func TestQuerySearchesWholeSnapshotAndSeparatesLiveAndDraftStates(t *testing.T) {
	s, scope, f, draft := setupCatalog(t)
	f.products = nil
	for i := range 25 {
		p := product(fmt.Sprint(100 + i))
		p.Data["status"] = "published"
		p.Data["description"] = strings.Repeat("large details ", 500)
		if i == 24 {
			p.Data["subject"] = "Unique kettle"
			p.Data["skuInfos"] = []any{map[string]any{"skuId": json.Number("9223372036854775806"), "cargoNumber": "WH-224"}}
		}
		f.products = append(f.products, p)
	}
	if _, err := s.Sync(context.Background(), scope, f, SyncInput{MaxPages: 10}); err != nil {
		t.Fatal(err)
	}
	page, err := s.QueryProducts(context.Background(), scope, Query{Query: "wh-224"})
	if err != nil || page.Total != 1 || page.Items[0].ID != "124" || page.Items[0].Status != "published" || page.Items[0].Stock != nil {
		t.Fatal(page, err)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "large details") || strings.Contains(string(raw), "skuInfos") {
		t.Fatal("summary contains heavy platform details")
	}
	first, _ := s.QueryProducts(context.Background(), scope, Query{Limit: 20})
	second, err := s.QueryProducts(context.Background(), scope, Query{Offset: 20, Limit: 20, Snapshot: first.Snapshot})
	if err != nil || len(second.Items) != 5 {
		t.Fatal(second, err)
	}
	subject := "Optimized kettle"
	if _, err := s.CreateDraft(context.Background(), scope, "124", Patch{Subject: &subject}); err != nil {
		t.Fatal(err)
	}
	withDraft, err := s.QueryProducts(context.Background(), scope, Query{HasDraft: true})
	if err != nil || withDraft.Total != 2 || !withDraft.Items[0].HasDraft {
		t.Fatal(withDraft, err, draft)
	}
	if _, err := s.QueryProducts(context.Background(), scope, Query{Offset: 20, Snapshot: first.Snapshot}); err == nil {
		t.Fatal("stale snapshot page was accepted")
	}
}

func TestLocalNewProductDraftCannotUseIncrementalPublication(t *testing.T) {
	s, scope, f, _ := setupCatalog(t)
	draft, err := s.IntakeDraft(context.Background(), scope, NewProductInput{Subject: "New source product", SourceURL: "https://detail.1688.com/offer/456.html"})
	if err != nil || draft.Kind != "create" || draft.ProductID != "" || draft.NewProduct == nil {
		t.Fatal(draft, err)
	}
	approved := false
	_, err = s.Publish(context.Background(), scope, f, draft.ID, draft.Revision, func(context.Context, Draft) error { approved = true; return nil })
	if err == nil || approved || f.reads != 0 || f.writes != 0 {
		t.Fatal("local intake was published through the update API", err, approved)
	}
	rows, err := s.QueryDrafts(context.Background(), scope)
	if err != nil || len(rows) != 2 || rows[0].Kind != "create" || rows[0].Title != "New source product" {
		t.Fatal(rows, err)
	}
}

func TestDesktopFacadeBrowsingAndStrictCredentialRequests(t *testing.T) {
	s, scope, f, _ := setupCatalog(t)
	target := Target{Scope: scope, Platform: func() (Platform, error) { return f, nil }, Authorization: AuthorizationStatus{Configured: true}, Capabilities: Capabilities{Supported: true, Read: true, EditDraft: true}}
	h := NewHTTPHandler(s, func(*http.Request) (Target, error) { return target, nil }, func(*http.Request) (Authorize, error) { return nil, errors.New("managed session required") })
	request := func(method, action, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/hoxi/work/catalog/"+action+"?storeId=store-a", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "products", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "Original title") {
		t.Fatal("browsing required a chat session", w.Code, w.Body.String())
	}
	w := request("GET", "product&unused=1", "")
	if w.Code == 200 {
		t.Fatal("invalid action accepted")
	}
	r := httptest.NewRequest("GET", "/api/hoxi/work/catalog/product?storeId=store-a&productId=123", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"skuId":"9223372036854775806"`) {
		t.Fatal("64-bit SKU ID lost precision at UI boundary", w.Body.String())
	}
	credentials, _ := json.Marshal(testCredentials())
	w = request("PUT", "authorization", string(credentials))
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-app-secret") || strings.Contains(w.Body.String(), "private-seller-token") {
		t.Fatal("credential endpoint leaked secrets", w.Body.String())
	}
	w = request("PUT", "authorization", `{"sellerLoginId":"factory-a","appKey":"123456","appSecret":"secret","accessToken":"token","extra":"must reject"}`)
	if w.Code != 400 {
		t.Fatal("unsupported credential field accepted")
	}
	if w := request("POST", "publish", `{"draftId":"missing","revision":"v1"}`); w.Code != 400 {
		t.Fatal("publication without managed session accepted")
	}
}

func TestCancelledBackgroundSyncResumesItsDurableCursor(t *testing.T) {
	s, scope, f, _ := setupCatalog(t)
	t.Cleanup(s.Close)
	f.products = nil
	for i := range 21 {
		f.products = append(f.products, product(fmt.Sprint(i+1)))
	}
	blocked := make(chan struct{})
	api := &blockingPagePlatform{fakePlatform: f, page: 2, entered: blocked}
	target := Target{Scope: scope, Platform: func() (Platform, error) { return api, nil }, Authorization: AuthorizationStatus{Configured: true}, Capabilities: Capabilities{Sync: true}}
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := s.StartSync(ctx, target, SyncInput{}); err != nil {
		t.Fatal(err)
	}
	cancel() // Navigating away must not cancel a desktop-owned sync job.
	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("background job did not continue after request ended")
	}
	if err := s.CancelSync(scope.StoreID); err != nil {
		t.Fatal(err)
	}
	job := waitCatalogJob(t, s, scope, "cancelled")
	if job.Fetched != 20 {
		t.Fatal("completed page progress was lost", job)
	}
	target.Platform = func() (Platform, error) { return f, nil }
	if _, err := s.StartSync(context.Background(), target, SyncInput{}); err != nil {
		t.Fatal(err)
	}
	job = waitCatalogJob(t, s, scope, "completed")
	if job.Fetched != 21 {
		t.Fatal("resume did not finish", job)
	}
	page, _ := s.QueryProducts(context.Background(), scope, Query{})
	if page.Total != 21 {
		t.Fatal("completed job did not swap the snapshot", page)
	}
}

type blockingPagePlatform struct {
	*fakePlatform
	page    int
	entered chan struct{}
}

func (p *blockingPagePlatform) List(ctx context.Context, page int, since, until time.Time) (Page, error) {
	if page == p.page {
		close(p.entered)
		<-ctx.Done()
		return Page{}, ctx.Err()
	}
	return p.fakePlatform.List(ctx, page, since, until)
}

func waitCatalogJob(t *testing.T, s *Service, scope Scope, status string) *SyncJob {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		job, err := s.SyncJobStatus(context.Background(), scope)
		if err != nil {
			t.Fatal(err)
		}
		if job != nil && job.Status == status {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("sync job did not reach", status)
	return nil
}

func TestStartSyncCleanupPreservesReplacementWorkerHandle(t *testing.T) {
	s, scope, f, _ := setupCatalog(t)
	t.Cleanup(s.Close)
	for round := range 8 {
		entered1 := make(chan struct{})
		api1 := &blockingPagePlatform{fakePlatform: f, page: 1, entered: entered1}
		target1 := Target{
			Scope:         scope,
			Platform:      func() (Platform, error) { return api1, nil },
			Authorization: AuthorizationStatus{Configured: true},
			Capabilities:  Capabilities{Sync: true},
		}
		if _, err := s.StartSync(context.Background(), target1, SyncInput{}); err != nil {
			t.Fatalf("round %d first StartSync: %v", round, err)
		}
		<-entered1
		if err := s.CancelSync(scope.StoreID); err != nil {
			t.Fatalf("round %d first CancelSync: %v", round, err)
		}

		entered2 := make(chan struct{})
		api2 := &blockingPagePlatform{fakePlatform: f, page: 1, entered: entered2}
		target2 := Target{
			Scope:         scope,
			Platform:      func() (Platform, error) { return api2, nil },
			Authorization: AuthorizationStatus{Configured: true},
			Capabilities:  Capabilities{Sync: true},
		}
		if _, err := s.StartSync(context.Background(), target2, SyncInput{}); err != nil {
			t.Fatalf("round %d replacement StartSync: %v", round, err)
		}
		<-entered2
		// Replacement worker handle must still be registered for CancelSync/Close.
		if err := s.CancelSync(scope.StoreID); err != nil {
			t.Fatalf("round %d replacement CancelSync lost worker handle: %v", round, err)
		}
		waitCatalogJob(t, s, scope, "cancelled")
	}
}
