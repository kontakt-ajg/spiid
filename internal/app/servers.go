package app

import (
	"context"
	"errors"
	"slices"
	"strings"

	"spiid/internal/config"
	"spiid/internal/errs"
)

// Servers returns a copy of the server list.
func (s *Service) Servers() []config.Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.servers)
}

func (s *Service) findServer(name string) (config.Server, bool) {
	for _, srv := range s.Servers() {
		if srv.Name == name {
			return srv, true
		}
	}
	return config.Server{}, false
}

func nameUsed(list []config.Server, name, except string) bool {
	for _, o := range list {
		if o.Name != except && strings.EqualFold(o.Name, name) {
			return true
		}
	}
	return false
}

// AddServer validates and stores a new server; secret (if not empty) is saved in config.json.
func (s *Service) AddServer(ctx context.Context, srv config.Server, secret string) error {
	srv.Normalize()
	if err := srv.Validate(); err != nil {
		return err
	}
	s.edit.Lock()
	defer s.edit.Unlock()
	list := s.Servers()
	if nameUsed(list, srv.Name, "") {
		return errs.New(errs.InvalidInput, "add server", srv.Name, errors.New("name already used"))
	}
	if srv.Auth != config.AuthAgent && secret != "" {
		if err := s.ssh.Unlock(ctx); err != nil {
			return err
		}
		return s.persistEdit(append(list, srv), srv.Name, srv.Name, &secret)
	}
	return s.persistEdit(append(list, srv), "", "", nil)
}

// UpdateServer replaces the server called oldName; secret == nil keeps the stored secret, "" deletes it.
func (s *Service) UpdateServer(ctx context.Context, oldName string, srv config.Server, secret *string) error {
	srv.Normalize()
	if err := srv.Validate(); err != nil {
		return err
	}
	s.edit.Lock()
	defer s.edit.Unlock()
	list := s.Servers()
	i := slices.IndexFunc(list, func(o config.Server) bool { return o.Name == oldName })
	if i < 0 {
		return errs.New(errs.InvalidInput, "update server", oldName, errors.New("unknown server"))
	}
	if nameUsed(list, srv.Name, oldName) {
		return errs.New(errs.InvalidInput, "update server", srv.Name, errors.New("name already used"))
	}
	list[i] = srv
	if secret != nil && *secret != "" && srv.Auth != config.AuthAgent {
		if err := s.ssh.Unlock(ctx); err != nil {
			return err
		}
	}
	if err := s.persistEdit(list, oldName, srv.Name, secret); err != nil {
		return err
	}
	s.ssh.RenamePool(oldName, srv.Name)
	return nil
}

// RemoveServer deletes a server and its saved secrets.
func (s *Service) RemoveServer(ctx context.Context, name string) error {
	s.edit.Lock()
	defer s.edit.Unlock()
	list := s.Servers()
	i := slices.IndexFunc(list, func(o config.Server) bool { return o.Name == name })
	if i < 0 {
		return errs.New(errs.InvalidInput, "remove server", name, errors.New("unknown server"))
	}
	return s.persistEdit(slices.Delete(list, i, i+1), "", "", nil)
}

func (s *Service) persistEdit(list []config.Server, oldName, name string, secret *string) error {
	if err := s.store.EditServers(list, oldName, name, secret); err != nil {
		return err
	}
	s.mu.Lock()
	s.servers = slices.Clone(list)
	s.mu.Unlock()
	s.ssh.ForgetSecrets("")
	return nil
}
