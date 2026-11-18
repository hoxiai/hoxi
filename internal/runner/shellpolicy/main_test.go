package shellpolicy

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	tmpHome, err := os.MkdirTemp("", "hoxi-shellpolicy-test-*")
	if err == nil {
		_ = os.Setenv("HOXI_HOME", tmpHome)
	}
	code := m.Run()
	if tmpHome != "" {
		_ = os.RemoveAll(tmpHome)
	}
	os.Exit(code)
}

func bashRequest(command string) map[string]interface{} {
	return map[string]interface{}{
		"permission": "bash",
		"patterns":   []interface{}{command},
		"metadata":   map[string]interface{}{"command": command},
	}
}
