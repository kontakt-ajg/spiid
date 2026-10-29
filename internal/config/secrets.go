package config

import (
	"errors"
	"strings"

	"spiid/internal/errs"
)

// Kind identifies which SSH authentication method uses a secret.
type Kind string

const (
	Password   Kind = "password"
	Passphrase Kind = "passphrase"
)

func fits(v Server, kind Kind) bool {
	return v.Auth == AuthPassword && kind == Password || v.Auth == AuthKey && kind == Passphrase
}
func (s *Store) Get(name string, kind Kind) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == "" || strings.ContainsAny(name, ":/") {
		return "", false, errs.New(errs.InvalidInput, "read secret", name, errors.New("bad server name"))
	}
	if err := s.load(); err != nil {
		return "", false, err
	}
	for _, v := range s.file.Servers {
		if v.Name != name || !fits(v.Server, kind) || v.Secret == "" {
			continue
		}
		if s.file.Master == nil {
			return v.Secret, true, nil
		}
		if s.key == nil {
			return "", false, errs.New(errs.Locked, "read secret", "", nil)
		}
		value, err := unseal(v.Secret, s.key)
		return value, err == nil, err
	}
	return "", false, nil
}
func (s *Store) Set(name string, kind Kind, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return err
	}
	next := s.file
	next.Servers = append([]savedServer(nil), next.Servers...)
	for i, v := range next.Servers {
		if v.Name != name || !fits(v.Server, kind) {
			continue
		}
		if value != "" && next.Master != nil {
			if s.key == nil {
				return errs.New(errs.Locked, "save secret", "", nil)
			}
			var err error
			value, err = seal(value, s.key)
			if err != nil {
				return err
			}
		}
		next.Servers[i].Secret = value
		if err := s.write(next); err != nil {
			return err
		}
		return nil
	}
	return errs.New(errs.InvalidInput, "save secret", name, errors.New("unknown server or authentication kind"))
}
func (s *Store) Delete(name string, kind Kind) error { return s.Set(name, kind, "") }
