package workcatalog

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestAdapterFactoryRegistrySupports1688AndCustomPlatforms(t *testing.T) {
	svc := NewService(NewStore(t.TempDir()))
	t.Cleanup(svc.Close)

	if got := svc.SupportedPlatforms(); !slices.Equal(got, []string{"1688"}) {
		t.Fatalf("expected default registered platform [1688], got %v", got)
	}

	// Unregistered platform resolves as unsupported and fails closed.
	unsupported, err := svc.ResolveTarget(context.Background(), StoreSettings{
		StoreID:  "store-shopify-1",
		Platform: "shopify",
	})
	if err != nil {
		t.Fatalf("ResolveTarget for unregistered platform returned unexpected error: %v", err)
	}
	if unsupported.Capabilities.Supported || unsupported.Authorization.Destination != "" {
		t.Fatalf("expected unsupported target for shopify, got %+v", unsupported)
	}
	if _, err := unsupported.Platform(); err == nil {
		t.Fatalf("expected unsupported platform factory to fail closed")
	}
	if _, err := svc.ConfigureAuthorizationForPlatform(context.Background(), "shopify", "store-shopify-1", testCredentials()); err == nil {
		t.Fatalf("expected ConfigureAuthorizationForPlatform on unregistered platform to fail")
	}

	// Registering a custom platform factory enables capabilities, validation, and client construction.
	fake := &fakePlatform{products: []Product{product("901")}}
	fake.products[0].Data["sellerLoginId"] = "merchant-alpha"
	svc.RegisterAdapter(AdapterSpec{
		Platform:    "MockShop",
		Destination: "https://api.mockshop.example",
		Capabilities: Capabilities{
			Supported:      true,
			Read:           true,
			Sync:           true,
			EditDraft:      true,
			NewDraft:       false,
			PublishUpdate:  true,
			PublishNew:     false,
			EditableFields: []string{"subject"},
		},
		ValidateCredentials: func(c Credentials) error {
			if c.SellerLoginID == "" || c.AppKey == "" || c.AccessToken == "" {
				return errors.New("mockshop requires sellerLoginId, appKey and accessToken")
			}
			return nil
		},
		ValidatePatch: func(p Patch) error {
			if p.Subject == nil || p.Description != nil || p.Images != nil {
				return errors.New("mockshop only allows subject updates")
			}
			return nil
		},
		NewFromCredentials: func(c Credentials, _ string) (Platform, error) {
			return fake, nil
		},
	})

	if got := svc.SupportedPlatforms(); !slices.Equal(got, []string{"1688", "mockshop"}) {
		t.Fatalf("expected [1688 mockshop], got %v", got)
	}

	authView, err := svc.ConfigureAuthorizationForPlatform(context.Background(), "mockshop", "store-mock-1", Credentials{
		SellerLoginID: "merchant-alpha",
		AppKey:        "mock_client_alpha",
		AccessToken:   "mock_secret_token",
	})
	if err != nil {
		t.Fatalf("ConfigureAuthorizationForPlatform failed: %v", err)
	}
	if !authView.Configured || authView.Destination != "https://api.mockshop.example" {
		t.Fatalf("unexpected custom platform auth view: %+v", authView)
	}

	target, err := svc.ResolveTarget(context.Background(), StoreSettings{
		StoreID:  "store-mock-1",
		Platform: "mockshop",
	})
	if err != nil {
		t.Fatalf("ResolveTarget failed: %v", err)
	}
	if !target.Capabilities.Supported || !slices.Equal(target.Capabilities.EditableFields, []string{"subject"}) {
		t.Fatalf("unexpected custom capabilities: %+v", target.Capabilities)
	}
	checked, err := svc.CheckAuthorization(context.Background(), target)
	if err != nil || !checked.VerifiedRead {
		t.Fatalf("CheckAuthorization failed: %v (%+v)", err, checked)
	}
	if _, err := svc.Sync(context.Background(), target.Scope, fake, SyncInput{MaxPages: 1}); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
	desc := "disallowed description"
	if _, err := svc.CreateDraftForPlatform(context.Background(), "mockshop", target.Scope, "901", Patch{Description: &desc}); err == nil {
		t.Fatalf("expected custom ValidatePatch to reject description patch")
	}
	subj := "Updated mockshop title"
	if _, err := svc.CreateDraftForPlatform(context.Background(), "mockshop", target.Scope, "901", Patch{Subject: &subj}); err != nil {
		t.Fatalf("expected custom ValidatePatch to accept subject patch, got %v", err)
	}
}
