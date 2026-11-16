package runner

// ActiveProcessCount includes processes whose session status may already be idle.
func (s *Service) ActiveProcessCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.procs)
}
