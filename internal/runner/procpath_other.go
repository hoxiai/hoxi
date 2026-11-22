//go:build !darwin || !cgo

package runner

func liveProcessExecutablePath() string { return "" }
