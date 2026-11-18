package memstore

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	tmpHome, err := os.MkdirTemp("", "hoxi-memstore-test-*")
	if err == nil {
		_ = os.Setenv("HOXI_HOME", tmpHome)
	}
	code := m.Run()
	if tmpHome != "" {
		_ = os.RemoveAll(tmpHome)
	}
	os.Exit(code)
}
