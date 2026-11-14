package workcatalog

import (
	"sort"
	"strings"
)

// AdapterSpec registers a platform-specific product catalog factory
// (destination, capabilities, credential/patch validation, and client builders).
type AdapterSpec struct {
	Platform            string
	Destination         string
	Capabilities        Capabilities
	ValidateCredentials func(Credentials) error
	ValidatePatch       func(Patch) error
	NewFromCredentials  func(Credentials, string) (Platform, error)
	NewFromEnv          func(string, string) (Platform, error)
}

func normalizePlatformID(platform string) string {
	return strings.ToLower(strings.TrimSpace(platform))
}

func cloneCapabilities(in Capabilities) Capabilities {
	out := in
	if in.EditableFields != nil {
		out.EditableFields = append([]string(nil), in.EditableFields...)
	} else {
		out.EditableFields = []string{}
	}
	return out
}

func alibaba1688AdapterSpec() AdapterSpec {
	return AdapterSpec{
		Platform:    "1688",
		Destination: "https://gw.open.1688.com",
		Capabilities: Capabilities{
			Supported:      true,
			Read:           true,
			Sync:           true,
			EditDraft:      true,
			NewDraft:       true,
			PublishUpdate:  true,
			PublishNew:     false,
			EditableFields: []string{"subject", "description", "images"},
		},
		ValidateCredentials: validateCredentials,
		ValidatePatch:       validatePatch,
		NewFromCredentials: func(credentials Credentials, proxyURL string) (Platform, error) {
			return NewAlibaba1688(credentials, proxyURL)
		},
		NewFromEnv: func(prefix, proxyURL string) (Platform, error) {
			return NewAlibaba1688FromEnv(prefix, proxyURL)
		},
	}
}

// RegisterAdapter registers or replaces a platform catalog factory on Service.
func (s *Service) RegisterAdapter(spec AdapterSpec) {
	id := normalizePlatformID(spec.Platform)
	if id == "" {
		return
	}
	spec.Platform = id
	spec.Capabilities = cloneCapabilities(spec.Capabilities)
	s.adaptersMu.Lock()
	if s.adapters == nil {
		s.adapters = map[string]AdapterSpec{}
	}
	s.adapters[id] = spec
	s.adaptersMu.Unlock()
}

// Adapter looks up the registered catalog factory for platform.
func (s *Service) Adapter(platform string) (AdapterSpec, bool) {
	id := normalizePlatformID(platform)
	if id == "" {
		return AdapterSpec{}, false
	}
	s.adaptersMu.RLock()
	spec, ok := s.adapters[id]
	s.adaptersMu.RUnlock()
	if !ok {
		return AdapterSpec{}, false
	}
	spec.Capabilities = cloneCapabilities(spec.Capabilities)
	return spec, true
}

// SupportedPlatforms returns sorted IDs of all registered catalog adapters.
func (s *Service) SupportedPlatforms() []string {
	s.adaptersMu.RLock()
	ids := make([]string, 0, len(s.adapters))
	for id := range s.adapters {
		ids = append(ids, id)
	}
	s.adaptersMu.RUnlock()
	sort.Strings(ids)
	return ids
}
