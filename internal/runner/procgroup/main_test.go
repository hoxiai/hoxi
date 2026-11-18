package procgroup

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	tmpHome, err := os.MkdirTemp("", "hoxi-procgroup-test-home-*")
	if err != nil {
		panic("failed to create isolated HOXI_HOME for procgroup tests: " + err.Error())
	}
	_ = os.Setenv("HOXI_HOME", tmpHome)
	code := m.Run()
	_ = os.RemoveAll(tmpHome)
	os.Exit(code)
}
