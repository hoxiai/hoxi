package opencode

import (
	"os"
	"testing"

	"github.com/hoxiai/hoxi/internal/testutil/execstub"
)

// TestMain lets the test binary double as the fake CLIs made by execstub.Write.
func TestMain(m *testing.M) {
	execstub.Main()
	os.Exit(m.Run())
}
