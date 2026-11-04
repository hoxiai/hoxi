package runner

import (
	"strings"
	"sync"
)

// syncMap is a compile-time type-safe wrapper around sync.Map.
type syncMap[K comparable, V any] struct {
	m sync.Map
}

func (s *syncMap[K, V]) Load(key K) (V, bool) {
	v, ok := s.m.Load(key)
	if !ok {
		var zero V
		return zero, false
	}
	typed, ok := v.(V)
	return typed, ok
}

func (s *syncMap[K, V]) Store(key K, value V) {
	s.m.Store(key, value)
}

func (s *syncMap[K, V]) Delete(key K) {
	s.m.Delete(key)
}

func (s *syncMap[K, V]) LoadOrStore(key K, value V) (V, bool) {
	actual, loaded := s.m.LoadOrStore(key, value)
	typed, _ := actual.(V)
	return typed, loaded
}

func (s *syncMap[K, V]) LoadAndDelete(key K) (V, bool) {
	v, loaded := s.m.LoadAndDelete(key)
	if !loaded {
		var zero V
		return zero, false
	}
	typed, _ := v.(V)
	return typed, true
}

func (s *syncMap[K, V]) Swap(key K, value V) (V, bool) {
	prev, loaded := s.m.Swap(key, value)
	if !loaded {
		var zero V
		return zero, false
	}
	typed, _ := prev.(V)
	return typed, true
}

func (s *syncMap[K, V]) CompareAndSwap(key K, old, new V) bool {
	return s.m.CompareAndSwap(key, old, new)
}

func (s *syncMap[K, V]) CompareAndDelete(key K, old V) bool {
	return s.m.CompareAndDelete(key, old)
}

func (s *syncMap[K, V]) Range(fn func(key K, value V) bool) {
	s.m.Range(func(k, v any) bool {
		tk, okK := k.(K)
		tv, okV := v.(V)
		if !okK || !okV {
			return true
		}
		return fn(tk, tv)
	})
}

func deletePrefixedKeys[V any](m *syncMap[string, V], prefixes []string) {
	if len(prefixes) == 0 {
		return
	}
	m.Range(func(key string, _ V) bool {
		for _, pfx := range prefixes {
			if strings.HasPrefix(key, pfx) {
				m.Delete(key)
				break
			}
		}
		return true
	})
}
