package sshconn

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"spiid/internal/config"
	"spiid/internal/errs"
	"spiid/internal/sftptest"
)

func TestHostKeyAlgorithms(t *testing.T) {
	got := hostKeyAlgorithms([]string{"ssh-rsa", "ssh-ed25519-cert-v01@openssh.com"})
	if len(got) < 3 || got[0] != "rsa-sha2-512" || got[1] != "rsa-sha2-256" {
		t.Fatalf("rsa first: %v", got)
	}
	seen := map[string]bool{}
	for _, a := range got {
		if seen[a] {
			t.Fatalf("duplicate %q in %v", a, got)
		}
		seen[a] = true
	}
	if !seen["ssh-ed25519"] {
		t.Fatalf("rest of the supported algorithms missing: %v", got)
	}
	ed := hostKeyAlgorithms([]string{"ssh-ed25519", "ssh-ed25519"})
	if ed[0] != "ssh-ed25519" || len(ed) != len(hostKeyAlgorithms(nil)) {
		t.Fatalf("ed25519 first, no dups: %v", ed)
	}
	if !reflect.DeepEqual(hostKeyAlgorithms(nil), ssh.SupportedAlgorithms().HostKeys) {
		t.Fatal("no known types should give the supported list")
	}
}

func TestHostKeyFirstContactAccept(t *testing.T) {
	e := newEnv(t, sftptest.Options{})
	e.p.host = accept
	e.p.secret = answer("secret", false)
	m := e.manager(nil)
	c, err := m.Pool(e.srv, 1).Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	mustStat(t, c)
	if len(e.p.hosts) != 1 {
		t.Fatalf("%d host key prompts, want 1", len(e.p.hosts))
	}
	r := e.p.hosts[0]
	if r.Fingerprint != ssh.FingerprintSHA256(e.ss.HostKey()) || r.KeyType != "ssh-ed25519" ||
		r.Server != "box" || r.Host != e.ss.Host() || r.Port != e.ss.Port() {
		t.Fatalf("request = %+v", r)
	}
	data, err := os.ReadFile(e.known)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != e.ss.KnownHostsLine()+"\n" ||
		!strings.HasPrefix(string(data), fmt.Sprintf("[127.0.0.1]:%d ", e.ss.Port())) {
		t.Fatalf("known_hosts = %q", data)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(e.known)
		di, _ := os.Stat(filepath.Dir(e.known))
		if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
			t.Fatalf("modes: file %v, dir %v", fi.Mode().Perm(), di.Mode().Perm())
		}
	}
	m2 := e.manager(nil)
	c2, err := m2.Pool(e.srv, 1).Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	mustStat(t, c2)
	if len(e.p.hosts) != 1 {
		t.Fatalf("second manager prompted for the host key again")
	}
}

func TestHostKeyReject(t *testing.T) {
	e := newEnv(t, sftptest.Options{})
	e.p.host = func(context.Context, HostKeyRequest) (bool, error) { return false, nil }
	m := e.manager(nil)
	for i := 0; i < 2; i++ {
		_, err := m.Pool(e.srv, 1).Get(context.Background())
		mustKind(t, err, errs.HostKeyRejected)
	}
	if h, _ := e.p.counts(); h != 1 {
		t.Fatalf("%d host key prompts, want 1", h)
	}
	if _, err := os.Stat(e.known); err == nil {
		t.Fatal("known_hosts was created for a rejected key")
	}
}

func TestHostKeyMismatch(t *testing.T) {
	e := newEnv(t, sftptest.Options{})
	line := otherKeyLine(t, fmt.Sprintf("[127.0.0.1]:%d", e.ss.Port()))
	e.trust(line)
	before, _ := os.ReadFile(e.known)
	_, err := e.manager(nil).Pool(e.srv, 1).Get(context.Background())
	mustKind(t, err, errs.HostKeyMismatch)
	if !strings.Contains(err.Error(), "possible attack") {
		t.Fatalf("message = %q", err)
	}
	if h, s := e.p.counts(); h != 0 || s != 0 {
		t.Fatalf("prompted %d/%d times", h, s)
	}
	after, _ := os.ReadFile(e.known)
	if string(before) != string(after) {
		t.Fatal("known_hosts changed")
	}
}

func TestSaveHostKeyAddsMissingNewline(t *testing.T) {
	root := t.TempDir()
	srv := sftptest.Start(t, root, sftptest.Options{User: "u", Password: "pw"})
	home := t.TempDir()
	kh := filepath.Join(home, "known_hosts")
	pub, _, _ := ed25519.GenerateKey(nil)
	sk, _ := ssh.NewPublicKey(pub)
	other := strings.TrimSpace("other.example " + string(ssh.MarshalAuthorizedKey(sk)))
	os.WriteFile(kh, []byte(other), 0o600) // no trailing newline
	store, _ := config.Open(filepath.Join(home, "config.json"), nil)
	store.SaveServers([]config.Server{{Name: "s", Host: srv.Host(), Port: srv.Port(),
		User: "u", Auth: config.AuthPassword}})
	store.Set("s", config.Password, "pw")
	p := &probePrompter{t: t, answer: "pw"}
	m := NewManager(Options{Home: home, KnownHosts: kh, Credentials: store, Prompter: p,
		AgentConn: func() (io.ReadWriteCloser, error) { return nil, errors.New("no agent") }})
	defer m.Close()
	s := config.Server{Name: "s", Host: srv.Host(), Port: srv.Port(), User: "u", Auth: config.AuthPassword}
	c, err := m.Pool(s, 1).Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m.Pool(s, 1).Put(c)
	b, _ := os.ReadFile(kh)
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 2 || lines[0] != other {
		t.Fatalf("known_hosts corrupted:\n%s", b)
	}
}
