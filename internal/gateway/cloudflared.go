package gateway

import (
	"log/slog"

	"github.com/hoxiai/hoxi/internal/cloudflared"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// StartCloudflared enables the supervised Cloudflare Tunnel connector, loading
// its settings from configPath and (re)connecting when they already enable it.
func (g *Gateway) StartCloudflared(configPath string, logger *slog.Logger) {
	g.cloudflared.Configure(cloudflared.NewStore(configPath), "http://"+hoxipaths.DefaultRemoteIngressAddr, logger)
	g.cloudflared.Start()
}

// StopCloudflared shuts the connector down.
func (g *Gateway) StopCloudflared() { g.cloudflared.Stop() }
