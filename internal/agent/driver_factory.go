package agent

import (
	"fmt"
	"sync"

	"github.com/hoxiai/hoxi/internal/llm"
)

// DriverConstructor builds a ModelDriver instance for a registered protocol.
type DriverConstructor func() ModelDriver

// ProtocolFactory is a thread-safe registry factory for ModelDriver implementations.
type ProtocolFactory struct {
	mu           sync.RWMutex
	constructors map[llm.StreamProtocol]DriverConstructor
}

func newDriverFactory() *ProtocolFactory {
	f := &ProtocolFactory{
		constructors: make(map[llm.StreamProtocol]DriverConstructor),
	}
	f.Register(llm.ProtocolAnthropic, func() ModelDriver { return newAnthropicDriver() })
	f.Register(llm.ProtocolOpenAI, func() ModelDriver { return newOpenAIDriver(false) })
	f.Register(llm.ProtocolOpenAIResponses, func() ModelDriver { return newOpenAIDriver(true) })
	f.Register(llm.ProtocolGoogle, func() ModelDriver { return newGeminiDriver() })
	return f
}

// Register binds a protocol identifier to a driver constructor.
func (f *ProtocolFactory) Register(proto llm.StreamProtocol, ctor DriverConstructor) {
	if f == nil || ctor == nil {
		return
	}
	f.mu.Lock()
	f.constructors[proto] = ctor
	f.mu.Unlock()
}

// Create instantiates a ModelDriver for the given protocol.
func (f *ProtocolFactory) Create(proto llm.StreamProtocol) (ModelDriver, error) {
	if f == nil {
		return nil, fmt.Errorf("driver factory is nil")
	}
	f.mu.RLock()
	ctor, ok := f.constructors[proto]
	f.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unsupported stream protocol: %q", proto)
	}
	return ctor(), nil
}
