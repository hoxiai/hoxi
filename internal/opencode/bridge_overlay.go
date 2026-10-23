package opencode

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// mergeLocalBridgeProviders points providers served by Hoxi's own loopback
// bridge at this process's gateway. ~/.hoxi/models.json (OPENCODE_CONFIG) is
// shared by release, dev and headless processes and only carries a neutral
// release-address URL; OpenCode deep-merges OPENCODE_CONFIG_CONTENT over that
// file and re-reads both from its environment after /global/dispose, so each
// process's OpenCode keeps calling its own bridge.
//
// The entry is added even before Grok CLI is imported: OpenCode drops a
// provider with no models from /provider, and an import during this run must
// still reach this process's bridge without restarting OpenCode.
func mergeLocalBridgeProviders(config map[string]json.RawMessage, bridgeBase string) error {
	providers := map[string]json.RawMessage{}
	if raw, exists := config["provider"]; exists {
		if err := json.Unmarshal(raw, &providers); err != nil || providers == nil {
			return fmt.Errorf("OPENCODE_CONFIG_CONTENT provider must be an object")
		}
	}
	entry := map[string]json.RawMessage{}
	if raw, exists := providers["grok-cli"]; exists {
		if err := json.Unmarshal(raw, &entry); err != nil || entry == nil {
			return fmt.Errorf("OPENCODE_CONFIG_CONTENT provider.grok-cli must be an object")
		}
	}
	options := map[string]json.RawMessage{}
	if raw, exists := entry["options"]; exists {
		if err := json.Unmarshal(raw, &options); err != nil || options == nil {
			return fmt.Errorf("OPENCODE_CONFIG_CONTENT provider.grok-cli.options must be an object")
		}
	}
	options["baseURL"], _ = json.Marshal(strings.TrimRight(bridgeBase, "/") + hoxipaths.GrokCLIBridgePath)
	entry["options"], _ = json.Marshal(options)
	providers["grok-cli"], _ = json.Marshal(entry)
	config["provider"], _ = json.Marshal(providers)
	return nil
}
