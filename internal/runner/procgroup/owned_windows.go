//go:build windows

package procgroup

// PgrepPIDs has no Windows counterpart: pkill is never verified there, so
// the request keeps asking.
func PgrepPIDs(args []string) ([]int, bool) { return nil, false }
