package opencode

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/hoxiai/hoxi/internal/reclaim"
	"github.com/hoxiai/hoxi/internal/sandbox"
)

//go:embed browser_plugin.mjs
var browserPlugin []byte

//go:embed sandbox_plugin.mjs
var sandboxPlugin []byte

// PrepareBrowserPlugin creates a private, process-lifetime config overlay that
// carries the browser plugin, Hoxi's built-in agent modes and this process's
// local bridge provider URLs.
// Append env to the child environment and call cleanup only after the child exits.
// Existing persistent OpenCode configuration is never written or replaced.
// A non-nil ledger records the overlay directory so a later Hoxi start removes
// it if this process dies before cleanup runs.
func PrepareBrowserPlugin(baseURL, token string, ledger *reclaim.Ledger) (env []string, cleanup func(), err error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, nil, fmt.Errorf("invalid Hoxi browser base URL")
	}
	if strings.TrimSpace(token) == "" {
		return nil, nil, fmt.Errorf("Hoxi browser token is required")
	}
	config := map[string]json.RawMessage{}
	if raw, exists := os.LookupEnv("OPENCODE_CONFIG_CONTENT"); exists {
		if err := json.Unmarshal([]byte(raw), &config); err != nil || config == nil {
			return nil, nil, fmt.Errorf("OPENCODE_CONFIG_CONTENT must be a JSON object")
		}
	}
	var plugins []json.RawMessage
	if raw, exists := config["plugin"]; exists {
		if err := json.Unmarshal(raw, &plugins); err != nil || plugins == nil {
			return nil, nil, fmt.Errorf("OPENCODE_CONFIG_CONTENT plugin must be an array")
		}
	}
	dir, ledgerID, err := ledger.MkdirTemp("opencode-plugin", "hoxi-opencode-plugin-")
	if err != nil {
		return nil, nil, fmt.Errorf("create browser plugin directory: %w", err)
	}
	cleanup = func() {
		if os.RemoveAll(dir) == nil {
			ledger.Forget(ledgerID)
		}
	}
	defer func() {
		if err != nil {
			cleanup()
		}
	}()
	path := filepath.Join(dir, "plugin.mjs")
	if err = os.WriteFile(path, browserPlugin, 0600); err != nil {
		return nil, cleanup, fmt.Errorf("write browser plugin: %w", err)
	}
	pluginURL := fileURL(path)
	entry, _ := json.Marshal(pluginURL)
	plugins = append(plugins, entry)
	sandboxPath := filepath.Join(dir, "sandbox-plugin.mjs")
	if err = os.WriteFile(sandboxPath, sandboxPlugin, 0600); err != nil {
		return nil, cleanup, fmt.Errorf("write sandbox plugin: %w", err)
	}
	sandboxURL := fileURL(sandboxPath)
	sandboxEntry, _ := json.Marshal(sandboxURL)
	plugins = append(plugins, sandboxEntry)
	config["plugin"], _ = json.Marshal(plugins)
	if err = mergeBuiltinModes(config); err != nil {
		return nil, cleanup, err
	}
	if err = mergeLocalBridgeProviders(config, baseURL); err != nil {
		return nil, cleanup, err
	}
	config["$schema"] = json.RawMessage(`"https://opencode.ai/config.json"`)
	merged, err := json.Marshal(config)
	if err != nil {
		return nil, cleanup, fmt.Errorf("encode browser plugin overlay: %w", err)
	}
	return []string{
		"OPENCODE_CONFIG_CONTENT=" + string(merged),
		"HOXI_GATEWAY_URL=" + baseURL,
		"HOXI_BROWSER_URL=" + baseURL,
		"HOXI_BROWSER_TOKEN=" + token,
		"HOXI_SANDBOX_ROOT=" + sandbox.DefaultRoot(),
	}, cleanup, nil
}

func fileURL(path string) string {
	slash := strings.ReplaceAll(filepath.ToSlash(path), "\\", "/")
	if !strings.HasPrefix(slash, "/") {
		slash = "/" + slash
	}
	return (&url.URL{Scheme: "file", Path: slash}).String()
}
