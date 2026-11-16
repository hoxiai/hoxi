package cli

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
)

// SubcommandContext carries runtime streams and gateway bootstrap context for
// registered CLI subcommands.
type SubcommandContext struct {
	Args              []string
	Cwd               string
	DefaultBridgeAddr string
	BootHeadless      HeadlessBootFunc
	Stdin             io.Reader
	Stdout            io.Writer
	Stderr            io.Writer
}

// SubcommandHandler executes an extension CLI subcommand and returns an exit code.
type SubcommandHandler func(ctx context.Context, sc SubcommandContext) int

type registeredSubcommand struct {
	name     string
	helpLine string
	handler  SubcommandHandler
}

var (
	subcmdMu    sync.RWMutex
	subcmdOrder []string
	subcmdMap   = make(map[string]registeredSubcommand)
)

// RegisterSubcommand registers an optional CLI subcommand (e.g. from a local extension).
func RegisterSubcommand(name, helpLine string, handler SubcommandHandler) {
	key := strings.TrimSpace(name)
	if key == "" || handler == nil {
		return
	}
	subcmdMu.Lock()
	if _, exists := subcmdMap[key]; !exists {
		subcmdOrder = append(subcmdOrder, key)
	}
	subcmdMap[key] = registeredSubcommand{
		name:     key,
		helpLine: helpLine,
		handler:  handler,
	}
	subcmdMu.Unlock()
}

func isRegisteredSubcommand(name string) bool {
	subcmdMu.RLock()
	_, ok := subcmdMap[strings.TrimSpace(name)]
	subcmdMu.RUnlock()
	return ok
}

func lookupRegisteredSubcommand(name string) (SubcommandHandler, bool) {
	subcmdMu.RLock()
	entry, ok := subcmdMap[strings.TrimSpace(name)]
	subcmdMu.RUnlock()
	if !ok {
		return nil, false
	}
	return entry.handler, true
}

func registeredHelpLines() []string {
	subcmdMu.RLock()
	defer subcmdMu.RUnlock()
	var lines []string
	for _, key := range subcmdOrder {
		if line := strings.TrimRight(subcmdMap[key].helpLine, "\n"); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// ResolveSafeDocPath validates and resolves a document/spreadsheet path against
// credential and protected path guards.
func ResolveSafeDocPath(rawPath, cwd string) (string, error) {
	return resolveSafeDocPath(rawPath, cwd)
}

// WriteJSON encodes v as indented JSON to w.
func WriteJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
