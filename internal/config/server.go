// Package config holds the config folder layout, the server list and the settings.
package config

import (
	"errors"
	"net"
	"strconv"
	"strings"

	"spiid/internal/errs"
)

// AuthMethod is how a server login works.
type AuthMethod string

// The login methods.
const (
	AuthAgent    AuthMethod = "agent"
	AuthKey      AuthMethod = "key"
	AuthPassword AuthMethod = "password"
)

// Server is one saved SFTP server.
type Server struct {
	Name     string     `json:"name"`
	Host     string     `json:"host"`
	Port     int        `json:"port"`
	User     string     `json:"user"`
	Auth     AuthMethod `json:"auth"`
	KeyFile  string     `json:"key_file,omitempty"`  // may start with "~/"
	StartDir string     `json:"start_dir,omitempty"` // "" = login folder
}

// Normalize trims spaces, sets Port 0 to 22 and clears KeyFile unless Auth is AuthKey.
func (s *Server) Normalize() {
	s.Name = strings.TrimSpace(s.Name)
	s.Host = strings.TrimSpace(s.Host)
	s.User = strings.TrimSpace(s.User)
	s.KeyFile = strings.TrimSpace(s.KeyFile)
	s.StartDir = strings.TrimSpace(s.StartDir)
	if s.Port == 0 {
		s.Port = 22
	}
	if s.Auth != AuthKey {
		s.KeyFile = ""
	}
}

// Validate checks a normalized server and returns an errs.InvalidInput error naming the first problem.
func (s Server) Validate() error {
	reason := s.problem()
	if reason == "" {
		return nil
	}
	return errs.New(errs.InvalidInput, "validate server", s.Name, errors.New(reason))
}

func (s Server) problem() string {
	switch {
	case s.Name == "":
		return "name is empty"
	case strings.ContainsAny(s.Name, ":/"):
		return "name must not contain ':' or '/'"
	case s.Host == "":
		return "host is empty"
	case s.User == "":
		return "user is empty"
	case s.Port < 1 || s.Port > 65535:
		return "port must be 1–65535"
	}
	switch s.Auth {
	case AuthAgent, AuthPassword:
	case AuthKey:
		if s.KeyFile == "" {
			return "key file is empty"
		}
	default:
		return "unknown login method"
	}
	return ""
}

// Addr returns "host:port".
func (s Server) Addr() string {
	return net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
}
