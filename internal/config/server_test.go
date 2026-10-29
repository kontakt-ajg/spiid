package config

import (
	"strings"
	"testing"

	"spiid/internal/errs"
)

func TestServerValidate(t *testing.T) {
	ok := Server{Name: "a", Host: "h", Port: 22, User: "u", Auth: AuthPassword}
	tests := []struct {
		name   string
		mod    func(s *Server)
		reason string
	}{
		{"valid", func(s *Server) {}, ""},
		{"valid key", func(s *Server) { s.Auth = AuthKey; s.KeyFile = "~/k" }, ""},
		{"name empty", func(s *Server) { s.Name = "" }, "name is empty"},
		{"name colon", func(s *Server) { s.Name = "a:b" }, "name must not contain ':' or '/'"},
		{"name slash", func(s *Server) { s.Name = "a/b" }, "name must not contain ':' or '/'"},
		{"host empty", func(s *Server) { s.Host = "" }, "host is empty"},
		{"user empty", func(s *Server) { s.User = "" }, "user is empty"},
		{"port low", func(s *Server) { s.Port = 0 }, "port must be 1–65535"},
		{"port high", func(s *Server) { s.Port = 65536 }, "port must be 1–65535"},
		{"auth", func(s *Server) { s.Auth = "x" }, "unknown login method"},
		{"key empty", func(s *Server) { s.Auth = AuthKey }, "key file is empty"},
	}
	for _, tt := range tests {
		s := ok
		tt.mod(&s)
		err := s.Validate()
		if tt.reason == "" {
			if err != nil {
				t.Errorf("%s: %v", tt.name, err)
			}
			continue
		}
		if err == nil || errs.KindOf(err) != errs.InvalidInput || !strings.Contains(err.Error(), tt.reason) {
			t.Errorf("%s: got %v", tt.name, err)
		}
	}
}

func TestServerNormalize(t *testing.T) {
	s := Server{Name: " a ", Host: " h ", User: " u ", Auth: AuthPassword, KeyFile: " k ", StartDir: " /x "}
	s.Normalize()
	want := Server{Name: "a", Host: "h", Port: 22, User: "u", Auth: AuthPassword, StartDir: "/x"}
	if s != want {
		t.Errorf("got %+v", s)
	}
	k := Server{Auth: AuthKey, KeyFile: " ~/k ", Port: 2222}
	k.Normalize()
	if k.KeyFile != "~/k" || k.Port != 2222 {
		t.Errorf("got %+v", k)
	}
	if a := (Server{Host: "::1", Port: 22}).Addr(); a != "[::1]:22" {
		t.Errorf("addr %q", a)
	}
}
