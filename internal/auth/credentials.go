package auth

import "strings"

// DeviceTokenSnapshot keeps the token and its destination from one account state.
func (s *Service) DeviceTokenSnapshot() (token, baseURL string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.current != nil {
		token = s.current.DeviceToken
	}
	return token, s.serverBaseURL
}

// WithCurrentDeviceToken commits account-owned state only while the initiating
// account is still current. Logout, invalidation and account replacement wait
// for the commit and clear its output before they finish. The callback must not
// start another account change or call this method recursively.
func (s *Service) WithCurrentDeviceToken(token, baseURL string, commit func() error) (bool, error) {
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	s.mu.RLock()
	valid := token != "" && s.current != nil && s.current.DeviceToken == token &&
		s.serverBaseURL == strings.TrimRight(baseURL, "/")
	s.mu.RUnlock()
	if !valid {
		return false, nil
	}
	return true, commit()
}

func (s *Service) invalidateDeviceToken(token, baseURL string, generation *uint64) error {
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	s.mu.Lock()
	if token == "" || s.current == nil || s.current.DeviceToken != token ||
		(baseURL != "" && s.serverBaseURL != strings.TrimRight(baseURL, "/")) ||
		(generation != nil && s.generation != *generation) {
		s.mu.Unlock()
		return nil
	}
	if err := s.store.ClearForServer(s.serverBaseURL, token); err != nil {
		s.mu.Unlock()
		return err
	}
	s.current = nil
	s.generation++
	onClear := s.onClear
	s.mu.Unlock()
	if onClear != nil {
		return onClear()
	}
	return nil
}
