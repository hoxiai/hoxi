package runner

import "sync"

// sessionTurnLocks serializes mutations of one conversation without holding up
// unrelated conversations during network, filesystem or process operations.
type sessionTurnLocks struct {
	mu      sync.Mutex
	entries map[string]*sessionTurnLock
}

type sessionTurnLock struct {
	mu    sync.Mutex
	users int
}

func (locks *sessionTurnLocks) lock(id string) func() {
	locks.mu.Lock()
	if locks.entries == nil {
		locks.entries = make(map[string]*sessionTurnLock)
	}
	entry := locks.entries[id]
	if entry == nil {
		entry = &sessionTurnLock{}
		locks.entries[id] = entry
	}
	entry.users++
	locks.mu.Unlock()
	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		locks.mu.Lock()
		entry.users--
		if entry.users == 0 {
			delete(locks.entries, id)
		}
		locks.mu.Unlock()
	}
}
