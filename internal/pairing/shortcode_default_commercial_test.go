//go:build !oss

package pairing

import "testing"

func TestDefaultShortCodeRelayURLCommercial(t *testing.T) {
	t.Setenv("HOXI_PAIRING_RELAY_URL", "")
	if got := DefaultShortCodeRelayURL(); got != "https://link.hoxi.ai" {
		t.Fatalf("DefaultShortCodeRelayURL() = %q, want https://link.hoxi.ai", got)
	}

	t.Setenv("HOXI_PAIRING_RELAY_URL", "https://custom.relay.example/")
	if got := DefaultShortCodeRelayURL(); got != "https://custom.relay.example" {
		t.Fatalf("DefaultShortCodeRelayURL() with env = %q, want https://custom.relay.example", got)
	}
}
