//go:build oss

package app

import (
	"testing"

	"github.com/hoxiai/hoxi/internal/llm"
)

func TestOSSBuildTagIsolatesOptionalSurfaceHooks(t *testing.T) {
	if len(optionalSurfaceHooks) != 0 {
		t.Fatalf("expected zero optionalSurfaceHooks under -tags oss, got %d", len(optionalSurfaceHooks))
	}
	mountOptionalSurface()
	if got := llm.ExtraBuiltinAgents(); len(got) != 0 {
		t.Fatalf("expected zero extra built-in agents under -tags oss, got %+v", got)
	}
}
