package turnstream

import (
	"os"
	"testing"

	"github.com/hoxiai/hoxi/internal/runner/sessionstore"
)

type Store = sessionstore.Store

func NewStore(customDir string) *Store { return sessionstore.NewStore(customDir) }

func init() {
	sessionstore.RegisterTranscriptHooks(sessionstore.TranscriptHooks{
		HydrateEdits:      hydrateAgyTranscriptEditParts,
		HydrateTurnTokens: hydrateAgyTranscriptTurnTokens,
		PreserveEditInput: preserveToolEditInput,
	})
}

func TestMain(m *testing.M) {
	tmpHome, err := os.MkdirTemp("", "hoxi-turnstream-test-*")
	if err == nil {
		_ = os.Setenv("HOXI_HOME", tmpHome)
	}
	code := m.Run()
	if tmpHome != "" {
		_ = os.RemoveAll(tmpHome)
	}
	os.Exit(code)
}
