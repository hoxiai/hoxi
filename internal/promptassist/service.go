// Package promptassist hosts the LLM helpers that work on a composer prompt:
// session title summarization and prompt enhancement. The gateway injects the
// llm.Client; requests go only to the model the user configured.
package promptassist

import (
	"encoding/json"
	"net/http"

	"github.com/hoxiai/hoxi/internal/llm"
)

// Service serves the prompt assist endpoints.
type Service struct {
	llm llm.Client
}

// NewService builds the assist handlers on top of client (nil disables the
// LLM path; titles then use the heuristic fallback).
func NewService(client llm.Client) *Service {
	return &Service{llm: client}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
