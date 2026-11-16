package gateway

import (
	"os"
	"testing"
)

// New wires stores that default to ~/.hoxi; keep tests off the real profile.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "hoxi-gateway-test-home-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("HOME", home)
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
