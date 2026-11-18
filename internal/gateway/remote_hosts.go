package gateway

import (
	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/remotehost"
)

func newDefaultRemoteHostService() *remotehost.Service {
	return remotehost.NewService(hoxipaths.RemoteHostsPath(), nil)
}

func registerRemoteHostRoutes(g *Gateway) {
	if g == nil || g.remoteHostMgr == nil {
		return
	}
	g.remoteHostMgr.RegisterRoutes(g.mux, isRemoteRequest)
}
