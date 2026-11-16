//go:build !darwin && !linux

package browser

import "testing"

func testProcessAlive(_ int) bool { return false }

func waitNoProfileProcesses(_ *testing.T, _ string) {}
