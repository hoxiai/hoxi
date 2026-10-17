//go:build !darwin

package main

func startDesktopStatusBar(_ *desktopApp)           {}
func updateDesktopStatusBar(_ desktopCodeSummary)   {}
func stopDesktopStatusBar()                         {}
