//go:build !darwin || !cgo

package app

// systemLocale falls back to English where the system language is not read.
func systemLocale() string { return "en-US" }
