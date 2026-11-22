//go:build !oss

package pairing

// defaultShortCodeRelayURL is the built-in pairing relay origin for official
// commercial builds. It can be overridden at link time via -ldflags or at
// runtime via the HOXI_PAIRING_RELAY_URL environment variable.
var defaultShortCodeRelayURL = "https://link.hoxi.ai"
