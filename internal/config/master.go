package config

import (
	"crypto/rand"
	"errors"
	"unicode/utf8"

	"spiid/internal/errs"
)

// Encrypted reports whether a master password is set.
func (s *Store) Encrypted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	return s.file.Master != nil
}

// Unlocked reports whether the master password is known for this run (always true without one).
func (s *Store) Unlocked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	return s.file.Master == nil || s.key != nil
}
func (s *Store) Unlock(current string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return err
	}
	if s.file.Master == nil {
		return nil
	}
	key, err := s.verify(current)
	if err != nil {
		return err
	}
	for _, v := range s.file.Servers {
		if v.Secret != "" {
			if _, err := unseal(v.Secret, key); err != nil {
				return err
			}
		}
	}
	s.key = key
	return nil
}
func minMaster(master string) error {
	if utf8.RuneCountInString(master) < 8 {
		return errs.New(errs.InvalidInput, "master password", "", errors.New("at least 8 characters"))
	}
	return nil
}

// Protect sets a master password (at least 8 characters) and encrypts every saved secret.
func (s *Store) Protect(master string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return err
	}
	if s.file.Master != nil {
		return errs.New(errs.InvalidInput, "protect", "", errors.New("already protected"))
	}
	return s.transform(nil, master, "set")
}

// ChangeMaster replaces the master password after checking current, re-encrypting every secret.
func (s *Store) ChangeMaster(current, master string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return err
	}
	key, err := s.verify(current)
	if err != nil {
		return err
	}
	return s.transform(key, master, "changed")
}

// Unprotect removes the master password after checking current; secrets become plain text.
func (s *Store) Unprotect(current string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return err
	}
	key, err := s.verify(current)
	if err != nil {
		return err
	}
	return s.transform(key, "", "removed")
}

// Reset removes the master password and deletes every saved secret (for a forgotten password).
func (s *Store) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return err
	}
	next := s.file
	next.Master = nil
	next.Servers = append([]savedServer(nil), next.Servers...)
	for i := range next.Servers {
		next.Servers[i].Secret = ""
	}
	if err := s.write(next); err != nil {
		return err
	}
	s.key = nil
	s.log.Info("config: master password reset — saved passwords deleted")
	return nil
}
func (s *Store) transform(oldKey []byte, password, action string) error {
	next := s.file
	next.Master = nil
	next.Servers = append([]savedServer(nil), next.Servers...)
	var key []byte
	if action != "removed" {
		if err := minMaster(password); err != nil {
			return err
		}
		m := defaultKDF
		m.Salt = make([]byte, 16)
		if _, err := rand.Read(m.Salt); err != nil {
			return err
		}
		key = m.derive(password)
		check, err := seal("spiid", key)
		if err != nil {
			return err
		}
		m.Check = check
		next.Master = &m
	}
	for i, v := range next.Servers {
		if v.Secret == "" {
			continue
		}
		value := v.Secret
		var err error
		if s.file.Master != nil {
			value, err = unseal(value, oldKey)
			if err != nil {
				return err
			}
		}
		if next.Master != nil {
			value, err = seal(value, key)
			if err != nil {
				return err
			}
		}
		next.Servers[i].Secret = value
	}
	if err := s.write(next); err != nil {
		return err
	}
	s.key = key
	s.log.Info("config: master password %s", action)
	return nil
}
