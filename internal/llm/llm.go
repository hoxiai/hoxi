// Package llm is the narrow contract for Hoxi-internal, single-shot LLM calls
// (session titles, commit messages, prompt rewrites, observer audits).
//
// It is a foundation package: provider.Service implements Client on top of its
// credential resolution and callSimpleLLM, and the gateway injects that Client
// into the domain packages that own each feature, so they never import
// provider. Every call goes straight to the user-configured provider; callers
// must not send session content anywhere else.
package llm

import (
	"context"
	"errors"
	"time"
)

// ErrUnavailable reports that no model with an HTTP endpoint is configured.
var ErrUnavailable = errors.New("no model is available")

// Client issues one bounded, non-streaming completion.
type Client interface {
	// Resolve maps request hints (empty or "auto" IDs, optional baseURL and
	// npm overrides from the UI) to a concrete model.
	Resolve(providerID, modelID, baseURL, npm string) Target
	// Complete sends prompt to target and returns the model's text answer.
	// It returns ErrUnavailable when target is not Ready.
	Complete(ctx context.Context, target Target, req Request) (string, error)
}

// Target is a resolved model. BaseURL and NPM carry the caller's overrides
// back to Complete; credentials never leave the provider package.
type Target struct {
	ProviderID string
	ModelID    string
	BaseURL    string
	NPM        string
	// HasEndpoint is true when the provider resolves to an http(s) endpoint.
	HasEndpoint bool
}

// HasModel reports whether both provider and model are known.
func (t Target) HasModel() bool { return t.ProviderID != "" && t.ModelID != "" }

// Ready reports whether Complete can be attempted.
func (t Target) Ready() bool { return t.HasModel() && t.HasEndpoint }

// Purpose selects the connection profile for a request.
type Purpose int

const (
	// PurposeInteractive is for UI-blocking helpers that should fail fast.
	PurposeInteractive Purpose = iota
	// PurposeReview is for observer audits that may wait a little longer to connect.
	PurposeReview
)

// Request is one completion prompt with its sampling budget.
type Request struct {
	Prompt      string
	MaxTokens   int
	Temperature float64
	Timeout     time.Duration
	Purpose     Purpose
	// ReasoningEffort asks reasoning models to think less ("low") so a short
	// answer fits MaxTokens. Providers that reject the parameter are retried
	// without it.
	ReasoningEffort string
}
