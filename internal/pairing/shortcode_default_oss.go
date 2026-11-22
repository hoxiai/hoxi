//go:build oss

package pairing

// defaultShortCodeRelayURL is intentionally empty in OSS builds so
// self-hosted users explicitly configure their own hoxi-pairing-relay
// (or inject a custom origin via -ldflags or HOXI_PAIRING_RELAY_URL).
var defaultShortCodeRelayURL = ""
