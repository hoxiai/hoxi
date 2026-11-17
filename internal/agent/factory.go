package agent

import "github.com/hoxiai/hoxi/internal/llm"

// NewDefaultEngine constructs an in-process Go Agent engine pre-wired with
// the protocol driver factory, built-in tool registry, and context compactor.
func NewDefaultEngine(stream llm.StreamClient) *Engine {
	return NewEngine(stream, newDriverFactory(), newToolRegistry(), pruneOldToolResults)
}
