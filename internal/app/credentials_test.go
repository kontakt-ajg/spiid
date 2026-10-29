package app

import (
	"context"
	"testing"

	"spiid/internal/errs"
	"spiid/internal/sshconn"
)

func TestCredentialsProtectChangeUnprotect(t *testing.T) {
	e := newEnv(t, sftpOpts())
	ctx := context.Background()
	s := e.service()
	if s.CredentialsEncrypted() {
		t.Fatal("new store should be plaintext")
	}
	if err := s.Protect(ctx, ""); err == nil {
		t.Fatal("empty master must fail")
	}
	if err := s.Protect(ctx, "master-password"); err != nil || !s.CredentialsEncrypted() {
		t.Fatalf("protect: %v", err)
	}
	// Current passwords are supplied explicitly, including when the store starts locked.
	s = e.service()
	if err := s.ChangeMaster(ctx, "master-password", "new-password"); err != nil {
		t.Fatal(err)
	}
	if n := e.p.count(sshconn.SecretMaster); n != 0 {
		t.Fatalf("%d master prompts, want 0", n)
	}
	s = e.service()
	if err := s.Unprotect(ctx, "new-password"); err != nil || s.CredentialsEncrypted() {
		t.Fatalf("unprotect: %v", err)
	}
	if n := e.p.count(sshconn.SecretMaster); n != 0 {
		t.Fatalf("%d master prompts, want 0", n)
	}
}

func TestNoPrompterCancelsUnlock(t *testing.T) {
	e := newEnv(t, sftpOpts())
	s := e.service()
	if err := s.Protect(context.Background(), "master-password"); err != nil {
		t.Fatal(err)
	}
	s2, err := New(Options{ConfigDir: e.cfg, Home: e.home, KnownHosts: e.known})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if err := s2.ssh.Unlock(context.Background()); errs.KindOf(err) != errs.Canceled {
		t.Fatalf("got %v", err)
	}
}
