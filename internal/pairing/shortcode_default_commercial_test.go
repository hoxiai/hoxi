//go:build !oss

package pairing

import "testing"

func TestDefaultShortCodeRelayURLCommercial(t *testing.T) {
	t.Setenv("HOXI_PAIRING_RELAY_URL", "")
	if got := DefaultShortCodeRelayURL(); got != "" {
		t.Fatalf("DefaultShortCodeRelayURL() = %q, want empty in base module", got)
	}

	svc := NewService("", 5417)
	svc.SetDefaultShortCodeRelayURL(func() string { return "https://ext.relay.example/" })
	if got := svc.effectiveShortCodeRelayURL(); got != "https://ext.relay.example" {
		t.Fatalf("effectiveShortCodeRelayURL() with extension = %q, want https://ext.relay.example", got)
	}

	t.Setenv("HOXI_PAIRING_RELAY_URL", "https://custom.relay.example/")
	if got := DefaultShortCodeRelayURL(); got != "https://custom.relay.example" {
		t.Fatalf("DefaultShortCodeRelayURL() with env = %q, want https://custom.relay.example", got)
	}
	if got := svc.effectiveShortCodeRelayURL(); got != "https://custom.relay.example" {
		t.Fatalf("effectiveShortCodeRelayURL() with env = %q, want https://custom.relay.example", got)
	}
}
