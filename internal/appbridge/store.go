package appbridge

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

const (
	TokenPrefix          = "hk_app_"
	EphemeralTokenPrefix = "hk_app_ephemeral_"
	lastUsedInterval     = time.Minute
	maxEphemeralTTL      = 24 * time.Hour
	maxGrants            = 4096
)

var (
	ErrInvalidToken  = errors.New("invalid application credentials")
	ErrGrantNotFound = errors.New("grant not found")
	ErrStorage       = errors.New("application grant storage unavailable")
	ErrQueueFull     = errors.New("application request queue is full")
)

type GrantInfo struct {
	ID                string   `json:"id"`
	AppID             string   `json:"appId"`
	Name              string   `json:"name"`
	Scopes            []string `json:"scopes"`
	AllowedWorkspaces []string `json:"allowedWorkspaces"`
	CreatedAt         int64    `json:"createdAt"`
	LastUsedAt        int64    `json:"lastUsedAt,omitempty"`
	ExpiresAt         int64    `json:"expiresAt,omitempty"`
	Ephemeral         bool     `json:"ephemeral,omitempty"`
}

func (g GrantInfo) clone() GrantInfo {
	g.Scopes = slices.Clone(g.Scopes)
	g.AllowedWorkspaces = slices.Clone(g.AllowedWorkspaces)
	return g
}

type GrantUpdate struct {
	Scopes            *[]string `json:"scopes"`
	AllowedWorkspaces *[]string `json:"allowedWorkspaces"`
}

type grantRecord struct {
	GrantInfo
	TokenHash string `json:"tokenHash"`
}

type grantDocument struct {
	Version int           `json:"version"`
	Grants  []grantRecord `json:"grants"`
}

type Service struct {
	path        string
	memoryMu    sync.Mutex
	ephemeral   map[string]grantRecord
	notify      map[string][]time.Time
	handshakeMu sync.Mutex
	connects    map[string]*connectState
	authCodes   map[string]*authCodeState
	approvalMu  sync.Mutex
	approvals   map[string]*approvalState
}

func NewService() *Service {
	return &Service{
		path: hoxipaths.AppGrantsPath(), ephemeral: make(map[string]grantRecord),
		notify: make(map[string][]time.Time), connects: make(map[string]*connectState),
		authCodes: make(map[string]*authCodeState), approvals: make(map[string]*approvalState),
	}
}

func randomHex(n int) (string, error) {
	data := make([]byte, n)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func sanitizeWorkspaces(paths []string) ([]string, error) {
	if len(paths) > 64 {
		return nil, errors.New("too many allowed workspaces")
	}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "*?") || strings.ContainsFunc(path, unicode.IsControl) || hoxipaths.IsDeniedDocOrSpreadsheetPath(path) {
			return nil, errors.New("allowed workspaces must be explicit, non-protected absolute paths")
		}
		path = filepath.Clean(path)
		if filepath.Dir(path) == path {
			return nil, errors.New("a volume root cannot be an allowed workspace")
		}
		out = append(out, path)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

func sanitizeAppName(appID, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return appID, nil
	}
	if len(name) > 96 || strings.ContainsFunc(name, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
		return "", errors.New("invalid application name")
	}
	return name, nil
}

func makeGrant(appID, name string, scopes, workspaces []string, ttl time.Duration) (grantRecord, string, error) {
	appID = strings.TrimSpace(appID)
	if !ValidAppID(appID) {
		return grantRecord{}, "", errors.New("invalid appId")
	}
	name, err := sanitizeAppName(appID, name)
	if err != nil {
		return grantRecord{}, "", err
	}
	scopes, err = NormalizeScopes(scopes)
	if err != nil {
		return grantRecord{}, "", err
	}
	workspaces, err = sanitizeWorkspaces(workspaces)
	if err != nil {
		return grantRecord{}, "", err
	}
	id, err := randomHex(16)
	if err != nil {
		return grantRecord{}, "", err
	}
	secret, err := randomHex(32)
	if err != nil {
		return grantRecord{}, "", err
	}
	now := time.Now()
	info := GrantInfo{ID: "agr_" + id, AppID: appID, Name: name, Scopes: scopes, AllowedWorkspaces: workspaces, CreatedAt: now.UnixMilli()}
	prefix := TokenPrefix
	if ttl > 0 {
		info.Ephemeral, info.ExpiresAt = true, now.Add(ttl).UnixMilli()
		prefix = EphemeralTokenPrefix
	}
	token := prefix + secret
	return grantRecord{GrantInfo: info, TokenHash: hashToken(token)}, token, nil
}

func (s *Service) CreateGrant(appID, name string, scopes, workspaces []string) (GrantInfo, string, error) {
	record, token, err := makeGrant(appID, name, scopes, workspaces, 0)
	if err != nil {
		return GrantInfo{}, "", err
	}
	err = s.withGrants(func(doc *grantDocument) (bool, error) {
		if len(doc.Grants) >= maxGrants {
			return false, ErrQueueFull
		}
		doc.Grants = append(doc.Grants, record)
		return true, nil
	})
	if err != nil {
		return GrantInfo{}, "", err
	}
	return record.GrantInfo, token, nil
}

func (s *Service) IssueEphemeralToken(appID, name string, scopes, workspaces []string, ttl time.Duration) (GrantInfo, string, error) {
	if ttl < time.Second || ttl > maxEphemeralTTL {
		return GrantInfo{}, "", errors.New("ephemeral token TTL must be between 1 second and 24 hours")
	}
	record, token, err := makeGrant(appID, name, scopes, workspaces, ttl)
	if err != nil {
		return GrantInfo{}, "", err
	}
	s.memoryMu.Lock()
	defer s.memoryMu.Unlock()
	s.pruneEphemeralLocked(time.Now().UnixMilli())
	if len(s.ephemeral) >= maxGrants {
		return GrantInfo{}, "", ErrQueueFull
	}
	s.ephemeral[record.ID] = record
	return record.GrantInfo.clone(), token, nil
}

func (s *Service) pruneEphemeralLocked(now int64) {
	for id, record := range s.ephemeral {
		if record.ExpiresAt <= now {
			delete(s.ephemeral, id)
		}
	}
}

func (s *Service) RevokeEphemeralToken(id string) error {
	s.memoryMu.Lock()
	if _, exists := s.ephemeral[id]; !exists {
		s.memoryMu.Unlock()
		return ErrGrantNotFound
	}
	delete(s.ephemeral, id)
	s.memoryMu.Unlock()
	s.cancelGrantApprovals(id)
	return nil
}

func (s *Service) ListGrants() ([]GrantInfo, error) {
	out := make([]GrantInfo, 0)
	err := s.withGrants(func(doc *grantDocument) (bool, error) {
		for _, record := range doc.Grants {
			out = append(out, record.GrantInfo)
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	s.memoryMu.Lock()
	s.pruneEphemeralLocked(time.Now().UnixMilli())
	for _, record := range s.ephemeral {
		out = append(out, record.GrantInfo.clone())
	}
	s.memoryMu.Unlock()
	slices.SortFunc(out, func(a, b GrantInfo) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// UpdateGrant only narrows an existing credential. Adding authority requires
// a fresh, desktop-approved handshake or explicit in-process issuance.
func (s *Service) UpdateGrant(id string, patch GrantUpdate) (GrantInfo, error) {
	var scopes, workspaces []string
	var err error
	if patch.Scopes == nil && patch.AllowedWorkspaces == nil {
		return GrantInfo{}, errors.New("no grant changes supplied")
	}
	if patch.Scopes != nil {
		scopes, err = NormalizeScopes(*patch.Scopes)
		if err != nil {
			return GrantInfo{}, err
		}
	}
	if patch.AllowedWorkspaces != nil {
		workspaces, err = sanitizeWorkspaces(*patch.AllowedWorkspaces)
		if err != nil {
			return GrantInfo{}, err
		}
	}
	var updated GrantInfo
	apply := func(record *grantRecord) error {
		for _, sc := range scopes {
			if !HasScope(record.Scopes, sc) {
				return errors.New("scope expansion requires new consent")
			}
		}
		for _, dir := range workspaces {
			if !slices.Contains(record.AllowedWorkspaces, dir) {
				return errors.New("workspace expansion requires new consent")
			}
		}
		if patch.Scopes != nil {
			record.Scopes = scopes
		}
		if patch.AllowedWorkspaces != nil {
			record.AllowedWorkspaces = workspaces
		}
		updated = record.GrantInfo.clone()
		return nil
	}
	s.memoryMu.Lock()
	s.pruneEphemeralLocked(time.Now().UnixMilli())
	if record, ok := s.ephemeral[id]; ok {
		err := apply(&record)
		if err == nil {
			s.ephemeral[id] = record
		}
		s.memoryMu.Unlock()
		if err == nil && !HasScope(updated.Scopes, ScopeApprovalRequest) {
			s.cancelGrantApprovals(id)
		}
		return updated, err
	}
	s.memoryMu.Unlock()
	err = s.withGrants(func(doc *grantDocument) (bool, error) {
		for i := range doc.Grants {
			if doc.Grants[i].ID == id {
				return true, apply(&doc.Grants[i])
			}
		}
		return false, ErrGrantNotFound
	})
	if err != nil {
		return GrantInfo{}, err
	}
	if !HasScope(updated.Scopes, ScopeApprovalRequest) {
		s.cancelGrantApprovals(id)
	}
	return updated, nil
}

func (s *Service) RevokeGrant(id string) error {
	if s.RevokeEphemeralToken(id) == nil {
		return nil
	}
	err := s.withGrants(func(doc *grantDocument) (bool, error) {
		for i, record := range doc.Grants {
			if record.ID == id {
				doc.Grants = slices.Delete(doc.Grants, i, i+1)
				return true, nil
			}
		}
		return false, ErrGrantNotFound
	})
	if err == nil {
		s.cancelGrantApprovals(id)
	}
	return err
}

func (s *Service) VerifyToken(token, appID string) (GrantInfo, error) {
	ephemeral := strings.HasPrefix(token, EphemeralTokenPrefix)
	prefix := TokenPrefix
	if ephemeral {
		prefix = EphemeralTokenPrefix
	}
	if len(token) != len(prefix)+64 || !strings.HasPrefix(token, prefix) || appID != strings.TrimSpace(appID) || !ValidAppID(appID) {
		return GrantInfo{}, ErrInvalidToken
	}
	digest := hashToken(token)
	matches := func(record grantRecord) bool {
		return subtle.ConstantTimeCompare([]byte(digest), []byte(record.TokenHash)) == 1 && record.AppID == appID
	}
	var info GrantInfo
	if ephemeral {
		s.memoryMu.Lock()
		defer s.memoryMu.Unlock()
		now := time.Now().UnixMilli()
		s.pruneEphemeralLocked(now)
		for id, record := range s.ephemeral {
			if matches(record) {
				if now-record.LastUsedAt >= lastUsedInterval.Milliseconds() {
					record.LastUsedAt = now
					s.ephemeral[id] = record
				}
				info = record.GrantInfo.clone()
			}
		}
	} else if err := s.withGrants(func(doc *grantDocument) (bool, error) {
		now := time.Now().UnixMilli()
		changed := false
		for i, record := range doc.Grants {
			if matches(record) {
				if now-record.LastUsedAt >= lastUsedInterval.Milliseconds() {
					record.LastUsedAt, changed = now, true
					doc.Grants[i] = record
				}
				info = record.GrantInfo
			}
		}
		return changed, nil
	}); err != nil {
		return GrantInfo{}, err
	}
	if info.ID == "" {
		return GrantInfo{}, ErrInvalidToken
	}
	return info, nil
}

// AllowNotify rate-limits an app identity across all its grants, including
// freshly issued ephemeral tokens.
func (s *Service) AllowNotify(appID string) bool {
	if !ValidAppID(appID) {
		return false
	}
	return s.allowNotify(appID, time.Now())
}

func (s *Service) allowNotify(appID string, now time.Time) bool {
	s.memoryMu.Lock()
	defer s.memoryMu.Unlock()
	cutoff := now.Add(-time.Minute)
	for id, times := range s.notify {
		i := 0
		for i < len(times) && !times[i].After(cutoff) {
			i++
		}
		if i == len(times) {
			delete(s.notify, id)
		} else {
			s.notify[id] = times[i:]
		}
	}
	if len(s.notify[appID]) >= 10 || (len(s.notify[appID]) == 0 && len(s.notify) >= maxGrants) {
		return false
	}
	s.notify[appID] = append(s.notify[appID], now)
	return true
}

func (s *Service) withGrants(fn func(*grantDocument) (bool, error)) error {
	var operationErr error
	err := hoxipaths.WithStateFileLock(s.path, func() error {
		doc, err := readGrants(s.path)
		if err != nil {
			return err
		}
		var changed bool
		changed, operationErr = fn(&doc)
		if operationErr != nil || !changed {
			return operationErr
		}
		return hoxipaths.WriteJSONAtomic(s.path, doc, 0o600)
	})
	if err != nil && operationErr == nil {
		return fmt.Errorf("%w: %w", ErrStorage, err)
	}
	return err
}

func readGrants(path string) (grantDocument, error) {
	doc := grantDocument{Version: 1, Grants: []grantRecord{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return doc, nil
	}
	if err != nil {
		return grantDocument{}, err
	}
	doc = grantDocument{}
	if err := json.Unmarshal(raw, &doc); err == nil && validGrantDocument(doc) {
		return doc, nil
	}
	// Keep the original in place: a subsequent boot must not mistake corruption
	// for an empty store. One private backup per distinct corrupt snapshot.
	backup := path + ".corrupt-" + hashToken(string(raw))[:16]
	if _, err := os.Lstat(backup); errors.Is(err, os.ErrNotExist) {
		if err := hoxipaths.WriteFileAtomic(backup, raw, 0o600); err != nil {
			return grantDocument{}, err
		}
	} else if err != nil {
		return grantDocument{}, err
	}
	return grantDocument{}, errors.New("invalid grant document; original and private backup retained")
}

func validGrantDocument(doc grantDocument) bool {
	if doc.Version != 1 || doc.Grants == nil || len(doc.Grants) > maxGrants {
		return false
	}
	ids, hashes := make(map[string]bool), make(map[string]bool)
	for _, g := range doc.Grants {
		hash, err := hex.DecodeString(g.TokenHash)
		id, idErr := hex.DecodeString(strings.TrimPrefix(g.ID, "agr_"))
		if err != nil || len(hash) != 32 || ids[g.ID] || hashes[g.TokenHash] || !strings.HasPrefix(g.ID, "agr_") || idErr != nil || len(id) != 16 || !ValidAppID(g.AppID) || g.Ephemeral || g.ExpiresAt != 0 || g.CreatedAt <= 0 {
			return false
		}
		if name, err := sanitizeAppName(g.AppID, g.Name); err != nil || name != g.Name {
			return false
		}
		if scopes, err := NormalizeScopes(g.Scopes); err != nil || !slices.Equal(scopes, g.Scopes) {
			return false
		}
		if dirs, err := sanitizeWorkspaces(g.AllowedWorkspaces); err != nil || !slices.Equal(dirs, g.AllowedWorkspaces) {
			return false
		}
		ids[g.ID], hashes[g.TokenHash] = true, true
	}
	return true
}
