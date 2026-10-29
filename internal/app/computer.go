package app

import (
	"context"

	"github.com/hoxiai/hoxi/internal/computer"
	"github.com/hoxiai/hoxi/internal/gateway"
)

func (a *DesktopApp) configureComputer(gw *gateway.Gateway) {
	gw.SetComputerAppConfirmation(func(request context.Context, _ computer.App, _ computer.Scope, _ string) (bool, error) {
		if err := request.Err(); err != nil {
			return false, err
		}
		return a.desktopContext() != nil, nil
	})
}
