//go:build oss

package pairing

import "testing"

func TestDefaultShortCodeRelayURLOSS(t *testing.T) {
	t.Setenv("HOXI_PAIRING_RELAY_URL", "")
	if got := DefaultShortCodeRelayURL(); got != "" {
		t.Fatalf("DefaultShortCodeRelayURL() in oss build = %q, want empty", got)
	}

	t.Setenv("HOXI_PAIRING_RELAY_URL", "https://selfhosted.relay.example/")
	if got := DefaultShortCodeRelayURL(); got != "https://selfhosted.relay.example" {
		t.Fatalf("DefaultShortCodeRelayURL() with env = %q, want https://selfhosted.relay.example", got)
	}
}
