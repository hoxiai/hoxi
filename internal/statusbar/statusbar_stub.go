//go:build !darwin

package statusbar

import "context"

func Start(_ context.Context) {}
func Update(_ Summary)        {}
func Stop()                   {}
