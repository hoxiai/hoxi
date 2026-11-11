package codeindex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/llm"
)

type stubSemanticEmbedder struct {
	calls int
}

func (s *stubSemanticEmbedder) HasEmbeddingModel() bool { return true }

func (s *stubSemanticEmbedder) EmbedTexts(_ context.Context, req llm.EmbeddingRequest) (llm.EmbeddingResponse, error) {
	s.calls++
	vecs := make([][]float32, len(req.Inputs))
	for i, text := range req.Inputs {
		lower := strings.ToLower(text)
		switch {
		case strings.Contains(text, "用户登录鉴权") || strings.Contains(lower, "verifyauthtoken") || strings.Contains(lower, "bearer jwt"):
			vecs[i] = []float32{0.96, 0.28, 0.0}
		case strings.Contains(lower, "renderinvoicepdf") || strings.Contains(lower, "invoice"):
			vecs[i] = []float32{0.05, 0.10, 0.99}
		default:
			vecs[i] = []float32{0.10, 0.95, 0.10}
		}
	}
	return llm.EmbeddingResponse{
		ProviderID: "local-emb",
		ModelID:    "bge-m3",
		Vectors:    vecs,
	}, nil
}

func TestHybridSemanticSearchRecallsCrossLanguageConcept(t *testing.T) {
	wsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(wsDir, "auth.go"), []byte(`package sample

// VerifyAuthToken validates incoming Bearer JWT credentials on API requests.
func VerifyAuthToken(header string) bool {
	return len(header) > 10
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "billing.go"), []byte(`package sample

func RenderInvoicePDF(orderID string) string {
	return orderID + ".pdf"
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewService(t.TempDir())

	// Without an embedder, a pure Chinese query against English identifiers has zero BM25 overlap.
	lexicalOnly, err := svc.Search(wsDir, "用户登录鉴权令牌校验", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(lexicalOnly.Results) != 0 {
		t.Fatalf("expected 0 pure lexical hits for cross-language query, got %+v", lexicalOnly.Results)
	}

	// With an embedder attached, semantic recall rescues VerifyAuthToken cleanly and caches chunk vectors.
	emb := &stubSemanticEmbedder{}
	svc.SetEmbedder(emb)

	hybridRes, err := svc.Search(wsDir, "用户登录鉴权令牌校验", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hybridRes.Results) == 0 || hybridRes.Results[0].Path != "auth.go" {
		t.Fatalf("expected auth.go to rank first via semantic recall, got %+v", hybridRes.Results)
	}

	// Repeating the exact same query should hit the semantic vector cache with zero extra network calls.
	callsAfterFirst := emb.calls
	hybridCached, err := svc.Search(wsDir, "用户登录鉴权令牌校验", 5)
	if err != nil || len(hybridCached.Results) == 0 || hybridCached.Results[0].Path != "auth.go" {
		t.Fatalf("expected cached semantic result for auth.go, got %+v err=%v", hybridCached.Results, err)
	}
	if emb.calls != callsAfterFirst {
		t.Fatalf("expected semantic vector cache hit without extra EmbedTexts call, calls %d -> %d", callsAfterFirst, emb.calls)
	}
}
