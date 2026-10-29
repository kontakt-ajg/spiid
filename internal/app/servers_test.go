package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"spiid/internal/config"
	"spiid/internal/errs"
)

func TestServersCopyAndValidation(t *testing.T) {
	e := newEnv(t, sftpOpts())
	s := e.service()
	ctx := context.Background()
	if err := s.AddServer(ctx, config.Server{Name: "a:b", Host: "h", User: "u", Auth: config.AuthAgent}, ""); errs.KindOf(
		err) != errs.InvalidInput {
		t.Fatalf("invalid name: %v", err)
	}
	srv := config.Server{Name: "One", Host: "h", User: "u", Auth: config.AuthAgent}
	if err := s.AddServer(ctx, srv, ""); err != nil {
		t.Fatal(err)
	}
	srv.Name = "one"
	if err := s.AddServer(ctx, srv, ""); errs.KindOf(err) != errs.InvalidInput {
		t.Fatalf("duplicate name (EqualFold): %v", err)
	}
	got := s.Servers()
	if len(got) != 1 || got[0].Port != 22 {
		t.Fatalf("servers %+v", got)
	}
	got[0].Host = "changed"
	if s.Servers()[0].Host != "h" {
		t.Fatal("Servers must return a copy")
	}
}

func TestUpdateRemoveErrors(t *testing.T) {
	e := newEnv(t, sftpOpts())
	s := e.service()
	ctx := context.Background()
	a := config.Server{Name: "a", Host: "h", User: "u", Auth: config.AuthAgent}
	b := config.Server{Name: "b", Host: "h", User: "u", Auth: config.AuthAgent}
	for _, x := range []config.Server{a, b} {
		if err := s.AddServer(ctx, x, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpdateServer(ctx, "nope", a, nil); errs.KindOf(err) != errs.InvalidInput {
		t.Fatalf("unknown old name: %v", err)
	}
	clash := a
	clash.Name = "B"
	if err := s.UpdateServer(ctx, "a", clash, nil); errs.KindOf(err) != errs.InvalidInput {
		t.Fatalf("name used by another: %v", err)
	}
	same := a
	same.Name = "A" // same server, only the case changes
	if err := s.UpdateServer(ctx, "a", same, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveServer(ctx, "nope"); errs.KindOf(err) != errs.InvalidInput {
		t.Fatalf("remove unknown: %v", err)
	}
}

func TestSecretHandling(t *testing.T) {
	e := newEnv(t, sftpOpts())
	s := e.service()
	ctx := context.Background()
	key := config.Server{Name: "k", Host: "h", User: "u", Auth: config.AuthKey, KeyFile: "~/id"}
	if err := s.AddServer(ctx, key, "phrase"); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := e.readCreds().Get("k", "passphrase"); !ok || v != "phrase" {
		t.Fatalf("passphrase %q %v", v, ok)
	}
	ag := config.Server{Name: "g", Host: "h", User: "u", Auth: config.AuthAgent}
	if err := s.AddServer(ctx, ag, "ignored"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := e.readCreds().Get("g", "password"); ok {
		t.Fatal("agent secret must be ignored")
	}
	empty, newer := "", "other"
	if err := s.UpdateServer(ctx, "k", key, &newer); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := e.readCreds().Get("k", "passphrase"); v != "other" {
		t.Fatalf("passphrase %q", v)
	}
	if err := s.UpdateServer(ctx, "k", key, &empty); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := e.readCreds().Get("k", "passphrase"); ok {
		t.Fatal("empty secret should delete")
	}
	if err := s.UpdateServer(ctx, "k", key, &newer); err != nil {
		t.Fatal(err)
	}
	pw := key
	pw.Auth, pw.KeyFile = config.AuthPassword, ""
	if err := s.UpdateServer(ctx, "k", pw, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := e.readCreds().Get("k", "passphrase"); ok {
		t.Fatal("passphrase should be deleted when the login method changes")
	}
}

func TestFailedServerEditsPreserveCredentials(t *testing.T) {
	for _, op := range []string{"add", "rename", "remove", "auth"} {
		t.Run(op, func(t *testing.T) {
			e := newEnv(t, sftpOpts())
			s := e.service()
			ctx := context.Background()
			srv := e.server(config.AuthPassword)
			if op != "add" {
				if err := s.AddServer(ctx, srv, "pw"); err != nil {
					t.Fatal(err)
				}
			}
			p := filepath.Join(e.cfg, "config.json")
			os.Remove(p)
			if err := os.MkdirAll(filepath.Join(p, "block"), 0700); err != nil {
				t.Fatal(err)
			}
			var err error
			switch op {
			case "add":
				err = s.AddServer(ctx, srv, "pw")
			case "rename":
				srv.Name = "new"
				err = s.UpdateServer(ctx, "box", srv, nil)
			case "remove":
				err = s.RemoveServer(ctx, "box")
			case "auth":
				srv.Auth = config.AuthAgent
				err = s.UpdateServer(ctx, "box", srv, nil)
			}
			if err == nil {
				t.Fatal("expected write failure")
			}
			c := s.store
			old, _, _ := c.Get("box", config.Password)
			newSecret, _, _ := c.Get("new", config.Password)
			t.Logf("error=%v; servers=%+v; box secret=%q; new secret=%q", err, s.Servers(), old, newSecret)
			if op == "add" {
				if old != "" {
					t.Error("failed add retained orphan secret")
				}
			} else if old != "pw" || newSecret != "" {
				t.Error("failed edit changed credentials while server list stayed unchanged")
			}
		})
	}
}

func TestAgentMetadataUpdateDoesNotUnlockCredentials(t *testing.T) {
	e := newEnv(t, sftpOpts())
	s := e.service()
	srv := e.server(config.AuthAgent)
	ctx := context.Background()
	if err := s.AddServer(ctx, srv, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Protect(ctx, "master-password"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = e.service()
	ignored := "unused"
	if err := s.UpdateServer(ctx, "box", srv, &ignored); err != nil {
		t.Fatal(err)
	}
	if n := e.p.count("master"); n != 0 {
		t.Fatalf("agent metadata update with ignored secret prompted master %d times", n)
	}
}
