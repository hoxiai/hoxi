//go:build !oss

package pairing

// defaultShortCodeRelayURL is empty in the open-source host module; commercial
// builds inject their unified relay origin via gateway.ShortCodeRelayExtension
// (or override via -ldflags / HOXI_PAIRING_RELAY_URL).
var defaultShortCodeRelayURL = ""
