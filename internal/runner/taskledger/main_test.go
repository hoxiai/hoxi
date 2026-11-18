package taskledger

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	tmpHome, err := os.MkdirTemp("", "hoxi-taskledger-test-home-*")
	if err != nil {
		panic("failed to create isolated HOXI_HOME for taskledger tests: " + err.Error())
	}
	_ = os.Setenv("HOXI_HOME", tmpHome)
	code := m.Run()
	_ = os.RemoveAll(tmpHome)
	os.Exit(code)
}
