package app

var optionalSurfaceHooks []func()

func mountOptionalSurface() {
	for _, hook := range optionalSurfaceHooks {
		if hook != nil {
			hook()
		}
	}
}
