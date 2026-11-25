package intake

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SetOnLinksChanged registers a callback invoked after an intake link is
// created or deleted so relay routers can refresh their hashed routing tables.
func (s *Service) SetOnLinksChanged(fn func()) {
	s.mu.Lock()
	s.onLinksChanged = fn
	s.mu.Unlock()
}

func (s *Service) notifyLinksChanged() {
	s.mu.Lock()
	fn := s.onLinksChanged
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// ActiveTokenHashes returns sorted SHA-256 hex digests of all active,
// non-expired intake portal tokens. Raw tokens are never exposed.
func (s *Service) ActiveTokenHashes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UnixMilli()
	out := make([]string, 0, len(s.links))
	for _, l := range s.links {
		if !l.Enabled || (l.ExpiresAt > 0 && now > l.ExpiresAt) {
			continue
		}
		tok := strings.TrimSpace(l.Token)
		if tok == "" {
			continue
		}
		sum := sha256.Sum256([]byte(tok))
		out = append(out, hex.EncodeToString(sum[:]))
	}
	sort.Strings(out)
	return out
}

func (s *Service) toPublicLink(l Link) PublicLink {
	portalPath := "/qa/" + l.Token
	pub := PublicLink{
		ID:          l.ID,
		Directory:   l.Directory,
		ProjectName: l.ProjectName,
		Label:       l.Label,
		Enabled:     l.Enabled,
		PortalPath:  portalPath,
		CreatedAt:   l.CreatedAt,
		UpdatedAt:   l.UpdatedAt,
		ExpiresAt:   l.ExpiresAt,
	}
	if s.endpoints != nil {
		ep := s.endpoints()
		if base := strings.TrimRight(strings.TrimSpace(ep.LocalBaseURL), "/"); base != "" {
			pub.LocalURL = base + portalPath
		}
		if base := strings.TrimRight(strings.TrimSpace(ep.LANBaseURL), "/"); base != "" {
			pub.LANURL = base + portalPath
		}
		if base := strings.TrimRight(strings.TrimSpace(ep.RelayBaseURL), "/"); base != "" {
			pub.RelayURL = base + portalPath
		}
	}
	return pub
}

// AuthorizePortalToken performs constant-time lookup of an active, non-expired link.
func (s *Service) AuthorizePortalToken(rawToken string) (Link, error) {
	tok := strings.TrimSpace(rawToken)
	if !IsValidPortalTokenFormat(tok) {
		return Link{}, ErrUnauthorizedToken
	}
	tokBytes := []byte(tok)

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UnixMilli()
	var matched Link
	found := false
	for _, l := range s.links {
		cand := []byte(l.Token)
		if len(cand) == len(tokBytes) && subtle.ConstantTimeCompare(cand, tokBytes) == 1 {
			matched = l
			found = true
		}
	}
	if !found || !matched.Enabled {
		return Link{}, ErrUnauthorizedToken
	}
	if matched.ExpiresAt > 0 && now > matched.ExpiresAt {
		return Link{}, ErrUnauthorizedToken
	}
	return matched, nil
}

// CreateLink creates a new project-scoped tester intake link.
func (s *Service) CreateLink(in CreateLinkInput) (PublicLink, error) {
	dir, err := canonicalProjectDirectory(in.Directory)
	if err != nil {
		return PublicLink{}, errors.New("valid project directory is required")
	}
	tok, err := randomToken()
	if err != nil {
		return PublicLink{}, err
	}
	label := strings.TrimSpace(in.Label)
	if label == "" {
		label = "兼职测试"
	}
	if len(label) > 64 {
		label = label[:64]
	}
	projName := strings.TrimSpace(in.ProjectName)
	if projName == "" {
		projName = filepath.Base(dir)
	}

	s.mu.Lock()
	if s.loadErr != nil {
		s.mu.Unlock()
		return PublicLink{}, s.loadErr
	}
	now := s.now().UnixMilli()
	var expiresAt int64
	if in.ExpiresInHr > 0 {
		expiresAt = now + int64(in.ExpiresInHr)*int64(time.Hour/time.Millisecond)
	}
	link := Link{
		ID:          randomID("lnk_", 6),
		Token:       tok,
		Directory:   dir,
		ProjectName: projName,
		Label:       label,
		Enabled:     true,
		CreatedAt:   now,
		UpdatedAt:   now,
		ExpiresAt:   expiresAt,
	}
	s.links[link.ID] = link
	seq, snap := s.snapshotLocked()
	s.mu.Unlock()

	if err := s.store.saveSnapshot(seq, snap); err != nil {
		s.mu.Lock()
		delete(s.links, link.ID)
		s.mu.Unlock()
		return PublicLink{}, err
	}
	s.notifyLinksChanged()
	return s.toPublicLink(link), nil
}

// ListLinks lists intake links for directory (or all links when directory is empty).
func (s *Service) ListLinks(directory string) []PublicLink {
	canon := ""
	if strings.TrimSpace(directory) != "" {
		if c, err := canonicalProjectDirectory(directory); err == nil {
			canon = c
		} else {
			canon = filepath.Clean(strings.TrimSpace(directory))
		}
	}
	s.mu.Lock()
	out := make([]PublicLink, 0, len(s.links))
	for _, l := range s.links {
		if canon != "" && l.Directory != canon {
			continue
		}
		out = append(out, s.toPublicLink(l))
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

// DeleteLink permanently revokes and deletes an intake link by ID and sweeps its unreferenced orphan attachments.
func (s *Service) DeleteLink(id string) error {
	id = strings.TrimSpace(id)
	s.mu.Lock()
	if s.loadErr != nil {
		s.mu.Unlock()
		return s.loadErr
	}
	prevLink, ok := s.links[id]
	if !ok {
		s.mu.Unlock()
		return ErrLinkNotFound
	}
	referenced, _ := s.referencedAttachmentIDsLocked()
	var orphanIDs []string
	prevOrphanTimes := make(map[string]int64)
	for attID, owner := range s.attachmentLink {
		if owner == id && !referenced[attID] {
			orphanIDs = append(orphanIDs, attID)
			prevOrphanTimes[attID] = s.attachmentUploadedAt[attID]
			delete(s.attachmentLink, attID)
			delete(s.attachmentUploadedAt, attID)
		}
	}
	delete(s.links, id)
	delete(s.uploadHistory, id)
	seq, snap := s.snapshotLocked()
	s.mu.Unlock()

	if err := s.store.saveSnapshot(seq, snap); err != nil {
		s.mu.Lock()
		s.links[id] = prevLink
		for _, attID := range orphanIDs {
			s.attachmentLink[attID] = id
			if ts := prevOrphanTimes[attID]; ts > 0 {
				s.attachmentUploadedAt[attID] = ts
			}
		}
		s.mu.Unlock()
		return err
	}
	s.deleteAttachmentBlobs(orphanIDs)
	s.notifyLinksChanged()
	return nil
}
