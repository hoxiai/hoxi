//go:build windows

package runner

// pgrepPIDs has no Windows counterpart: pkill is never verified there, so
// the request keeps asking.
func pgrepPIDs(args []string) ([]int, bool) { return nil, false }
