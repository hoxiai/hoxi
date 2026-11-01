package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hoxiai/hoxi/internal/testutil/execstub"
)

// TestMain lets the test binary double as the fake CLIs made by execstub.Write
// and isolates default Hoxi home/database paths from the developer's machine.
func TestMain(m *testing.M) {
	execstub.Main()
	tmpHome, err := os.MkdirTemp("", "hoxi-runner-test-*")
	if err == nil {
		_ = os.Setenv("HOXI_HOME", tmpHome)
		_ = os.Setenv("HOXI_DB_PATH", filepath.Join(tmpHome, "hoxi.db"))
		_ = os.Setenv("OPENCODE_DB", filepath.Join(tmpHome, "hoxi.db"))
	}
	code := m.Run()
	if tmpHome != "" {
		_ = os.RemoveAll(tmpHome)
	}
	os.Exit(code)
}
