package app

import "spiid/internal/config"

// Settings returns the current settings.
func (s *Service) Settings() config.Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

// SaveSettings normalizes and saves st and applies the limits to jobs that start afterwards.
func (s *Service) SaveSettings(st config.Settings) error {
	s.edit.Lock()
	defer s.edit.Unlock()
	st.Normalize(s.home)
	if err := s.store.SaveSettings(st); err != nil {
		return err
	}
	s.mu.Lock()
	s.settings = st
	s.mu.Unlock()
	s.eng.SetLimits(st.ThreadsPerFile, st.ParallelFiles, st.MaxConnections)
	return nil
}
