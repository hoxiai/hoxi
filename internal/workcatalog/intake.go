package workcatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

// NewProductInput is a local intake draft, not an existing online product.
// Platform creation is a separate capability from incrementModify.
type NewProductInput struct {
	Subject     string   `json:"subject"`
	Description string   `json:"description"`
	Images      []string `json:"images"`
	CategoryID  string   `json:"categoryId"`
	SourceURL   string   `json:"sourceUrl,omitempty"`
}

func (s *Service) IntakeDraft(ctx context.Context, scope Scope, input NewProductInput) (Draft, error) {
	patch := Patch{Subject: &input.Subject}
	if input.Description != "" {
		patch.Description = &input.Description
	}
	if len(input.Images) > 0 {
		patch.Images = &input.Images
	}
	if err := validatePatch(patch); err != nil {
		return Draft{}, err
	}
	if input.CategoryID != "" && !productIDPattern.MatchString(input.CategoryID) {
		return Draft{}, errors.New("category ID must be numeric")
	}
	if len(input.SourceURL) > 2048 {
		return Draft{}, errors.New("source URL is too long")
	}
	id, err := newID("drf_")
	if err != nil {
		return Draft{}, err
	}
	draft := Draft{ID: id, Kind: "create", Base: Product{Data: map[string]any{}}, NewProduct: &input, Status: "draft", CreatedAt: time.Now().UTC(), Changes: []Change{}}
	raw, err := json.Marshal(draft)
	if err != nil {
		return Draft{}, err
	}
	sum := sha256.Sum256(raw)
	draft.Revision = hex.EncodeToString(sum[:])
	err = s.store.update(ctx, scope, func(st *state) error {
		if len(st.Drafts) >= 1000 {
			return errors.New("store draft limit reached (1000)")
		}
		st.Drafts[draft.ID] = draft
		return nil
	})
	return draft, err
}
