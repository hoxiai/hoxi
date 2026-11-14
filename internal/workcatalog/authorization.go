package workcatalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// Credentials are accepted only at the desktop configuration boundary, never
// returned by a query, MCP result, prompt or catalog record.
type Credentials struct {
	SellerLoginID string `json:"sellerLoginId"`
	AppKey        string `json:"appKey"`
	AppSecret     string `json:"appSecret"`
	AccessToken   string `json:"accessToken"`
}

type authorizationRecord struct {
	Version     int         `json:"version"`
	StoreID     string      `json:"storeId"`
	Credentials Credentials `json:"credentials"`
	VerifiedAt  *time.Time  `json:"verifiedAt,omitempty"`
}

type AuthorizationStatus struct {
	Source         string     `json:"source"`
	Configured     bool       `json:"configured"`
	VerifiedRead   bool       `json:"verifiedRead"`
	SellerLoginID  string     `json:"sellerLoginId"`
	AppKey         string     `json:"appKey"`
	HasAppSecret   bool       `json:"hasAppSecret"`
	HasAccessToken bool       `json:"hasAccessToken"`
	VerifiedAt     *time.Time `json:"verifiedAt,omitempty"`
	Destination    string     `json:"destination"`
}

type StoreSettings struct {
	StoreID       string
	Platform      string
	SellerLoginID string
	CredentialEnv string
	ProxyURL      string
	ProxyRequired bool
}

type Capabilities struct {
	Supported      bool     `json:"supported"`
	Read           bool     `json:"read"`
	Sync           bool     `json:"sync"`
	EditDraft      bool     `json:"editDraft"`
	NewDraft       bool     `json:"newDraft"`
	PublishUpdate  bool     `json:"publishUpdate"`
	PublishNew     bool     `json:"publishNew"`
	EditableFields []string `json:"editableFields"`
}

type Target struct {
	PlatformID          string
	Scope               Scope
	Platform            func() (Platform, error)
	Authorization       AuthorizationStatus
	Capabilities        Capabilities
	authorizationDigest [32]byte
}

func (s *Service) credentialValidator(platform string) func(Credentials) error {
	if spec, ok := s.Adapter(platform); ok && spec.ValidateCredentials != nil {
		return spec.ValidateCredentials
	}
	return validateCredentials
}

func (s *Service) readAuthorization(storeID string, validate func(Credentials) error) (*authorizationRecord, error) {
	if validate == nil {
		validate = validateCredentials
	}
	path, err := s.store.path(Scope{StoreID: storeID, SellerLoginID: "authorization"})
	if err != nil {
		return nil, err
	}
	path += ".auth.json"
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("cannot read local store authorization")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 32769))
	if err != nil || len(raw) > 32768 {
		return nil, errors.New("invalid local store authorization size")
	}
	var record authorizationRecord
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decodeErr := decoder.Decode(&record)
	var extra any
	if decodeErr != nil || decoder.Decode(&extra) != io.EOF || record.Version != 1 || record.StoreID != storeID || validate(record.Credentials) != nil {
		sum := sha256.Sum256(raw)
		if err := hoxipaths.WriteFileAtomic(path+".corrupt-"+hex.EncodeToString(sum[:]), raw, 0600); err != nil {
			return nil, errors.New("cannot back up invalid local store authorization")
		}
		return nil, errors.New("local store authorization is invalid; its backup and original were preserved")
	}
	return &record, nil
}

func validateCredentials(c Credentials) error {
	if c.SellerLoginID == "" || len(c.SellerLoginID) > 128 || c.SellerLoginID != strings.TrimSpace(c.SellerLoginID) || strings.ContainsAny(c.SellerLoginID, "\r\n\t") || !productIDPattern.MatchString(c.AppKey) {
		return errors.New("valid seller login ID and numeric application key are required")
	}
	for _, secret := range []string{c.AppSecret, c.AccessToken} {
		if secret == "" || len(secret) > 8192 || secret != strings.TrimSpace(secret) || strings.ContainsAny(secret, "\r\n\t") {
			return errors.New("application secret and access token are required and must not contain surrounding whitespace")
		}
	}
	return nil
}

func (s *Service) ConfigureAuthorization(ctx context.Context, storeID string, input Credentials) (AuthorizationStatus, error) {
	return s.ConfigureAuthorizationForPlatform(ctx, "1688", storeID, input)
}

func (s *Service) ConfigureAuthorizationForPlatform(ctx context.Context, platform, storeID string, input Credentials) (AuthorizationStatus, error) {
	if normalizePlatformID(platform) == "" {
		platform = "1688"
	}
	spec, supported := s.Adapter(platform)
	if !supported {
		return AuthorizationStatus{}, errors.New("this platform has no local catalog adapter")
	}
	validate := s.credentialValidator(platform)
	unlock, err := s.store.lock(ctx, Scope{StoreID: storeID, SellerLoginID: "authorization"}, ".authorization.lock")
	if err != nil {
		return AuthorizationStatus{}, err
	}
	defer unlock()
	previous, err := s.readAuthorization(storeID, validate)
	if err != nil {
		return AuthorizationStatus{}, err
	}
	if previous != nil && previous.Credentials.SellerLoginID == input.SellerLoginID && previous.Credentials.AppKey == input.AppKey {
		if input.AppSecret == "" {
			input.AppSecret = previous.Credentials.AppSecret
		}
		if input.AccessToken == "" {
			input.AccessToken = previous.Credentials.AccessToken
		}
	}
	if err := validate(input); err != nil {
		return AuthorizationStatus{}, err
	}
	// A credential update must not silently relink an existing catalog to a
	// different seller. Store snapshots and publication receipts keep their owner.
	if _, err := s.store.load(ctx, Scope{StoreID: storeID, SellerLoginID: input.SellerLoginID}); err != nil {
		return AuthorizationStatus{}, err
	}
	record := authorizationRecord{Version: 1, StoreID: storeID, Credentials: input}
	path, _ := s.store.path(Scope{StoreID: storeID, SellerLoginID: "authorization"})
	if err := hoxipaths.WriteJSONAtomic(path+".auth.json", record, 0600); err != nil {
		return AuthorizationStatus{}, errors.New("cannot save local store authorization")
	}
	return authorizationView(&record, spec.Destination), nil
}

func authorizationView(record *authorizationRecord, destination string) AuthorizationStatus {
	view := AuthorizationStatus{Source: "none", Destination: destination}
	if record != nil {
		view.Source, view.Configured = "local", true
		view.SellerLoginID, view.AppKey = record.Credentials.SellerLoginID, record.Credentials.AppKey
		view.HasAppSecret, view.HasAccessToken = true, true
		view.VerifiedAt, view.VerifiedRead = record.VerifiedAt, record.VerifiedAt != nil
	}
	return view
}

func (s *Service) ResolveTarget(ctx context.Context, settings StoreSettings) (Target, error) {
	platformID := normalizePlatformID(settings.Platform)
	spec, supported := s.Adapter(platformID)
	validate := s.credentialValidator(platformID)
	unlock, err := s.store.lock(ctx, Scope{StoreID: settings.StoreID, SellerLoginID: "authorization"}, ".authorization.lock")
	if err != nil {
		return Target{}, err
	}
	record, err := s.readAuthorization(settings.StoreID, validate)
	unlock()
	if err != nil {
		return Target{}, err
	}
	view := authorizationView(record, spec.Destination)
	seller := settings.SellerLoginID
	if record != nil {
		seller = record.Credentials.SellerLoginID
	}
	if seller == "" {
		seller = "unconfigured"
	}
	factory := func() (Platform, error) {
		if !supported {
			return nil, errors.New("this platform has no local catalog adapter")
		}
		if settings.ProxyRequired && settings.ProxyURL == "" {
			return nil, errors.New("the store requires a configured proxy")
		}
		if record != nil {
			if spec.NewFromCredentials == nil {
				return nil, errors.New("this platform has no local catalog adapter")
			}
			return spec.NewFromCredentials(record.Credentials, settings.ProxyURL)
		}
		if spec.NewFromEnv == nil {
			return nil, errors.New("store authorization is not configured")
		}
		return spec.NewFromEnv(settings.CredentialEnv, settings.ProxyURL)
	}
	if supported && record == nil && settings.SellerLoginID != "" {
		if _, err := factory(); err == nil {
			view.Source, view.Configured, view.SellerLoginID = "environment", true, seller
			view.HasAppSecret, view.HasAccessToken = true, true
		}
	}
	var digest [32]byte
	if record != nil {
		raw, _ := json.Marshal(record.Credentials)
		digest = sha256.Sum256(raw)
	}
	capabilities := Capabilities{Supported: false, EditableFields: []string{}}
	if supported {
		capabilities = spec.Capabilities
	}
	return Target{PlatformID: platformID, Scope: Scope{StoreID: settings.StoreID, SellerLoginID: seller}, Platform: factory,
		Authorization: view, authorizationDigest: digest, Capabilities: capabilities}, nil
}

func (s *Service) CheckAuthorization(ctx context.Context, target Target) (AuthorizationStatus, error) {
	api, err := target.Platform()
	if err != nil {
		return target.Authorization, err
	}
	page, err := api.List(ctx, 1, time.Time{}, time.Now())
	if err != nil {
		if target.Authorization.Source == "local" {
			if persistErr := s.markAuthorizationChecked(ctx, target, nil); persistErr != nil {
				return target.Authorization, persistErr
			}
		}
		return target.Authorization, err
	}
	for _, product := range page.Products {
		if err := validateOwnedProduct(product, target.Scope.SellerLoginID); err != nil {
			if target.Authorization.Source == "local" {
				if persistErr := s.markAuthorizationChecked(ctx, target, nil); persistErr != nil {
					return target.Authorization, persistErr
				}
			}
			return target.Authorization, err
		}
	}
	view := target.Authorization
	now := time.Now().UTC()
	view.VerifiedRead, view.VerifiedAt = true, &now
	if target.Authorization.Source == "local" {
		if err := s.markAuthorizationChecked(ctx, target, &now); err != nil {
			return target.Authorization, err
		}
	}
	return view, nil
}

func (s *Service) markAuthorizationChecked(ctx context.Context, target Target, when *time.Time) error {
	unlock, err := s.store.lock(ctx, target.Scope, ".authorization.lock")
	if err != nil {
		return err
	}
	defer unlock()
	record, err := s.readAuthorization(target.Scope.StoreID, s.credentialValidator(target.PlatformID))
	if err != nil {
		return err
	}
	if record == nil {
		return errors.New("store authorization changed during verification")
	}
	raw, err := json.Marshal(record.Credentials)
	if err != nil || sha256.Sum256(raw) != target.authorizationDigest {
		return errors.New("store authorization changed during verification")
	}
	record.VerifiedAt = when
	path, _ := s.store.path(target.Scope)
	if err := hoxipaths.WriteJSONAtomic(path+".auth.json", record, 0600); err != nil {
		return errors.New("cannot save authorization verification status")
	}
	return nil
}
