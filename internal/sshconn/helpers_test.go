package sshconn

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"spiid/internal/config"
	"spiid/internal/errs"
	"spiid/internal/sftptest"
)

// fakePrompter records every request and answers with the given funcs.
type fakePrompter struct {
	t       *testing.T
	inUse   atomic.Int32
	mu      sync.Mutex
	hosts   []HostKeyRequest
	secrets []SecretRequest
	host    func(ctx context.Context, r HostKeyRequest) (bool, error)
	secret  func(r SecretRequest, n int) (SecretAnswer, error)
}

func (p *fakePrompter) enter() func() {
	if p.inUse.Add(1) > 1 {
		p.t.Errorf("Prompter entered twice at once")
	}
	return func() { p.inUse.Add(-1) }
}

func (p *fakePrompter) ConfirmHostKey(ctx context.Context, r HostKeyRequest) (bool, error) {
	defer p.enter()()
	p.mu.Lock()
	p.hosts = append(p.hosts, r)
	p.mu.Unlock()
	if p.host == nil {
		p.t.Errorf("unexpected host key prompt")
		return false, errors.New("unexpected")
	}
	return p.host(ctx, r)
}

func (p *fakePrompter) Secret(ctx context.Context, r SecretRequest) (SecretAnswer, error) {
	defer p.enter()()
	p.mu.Lock()
	p.secrets = append(p.secrets, r)
	n := len(p.secrets)
	p.mu.Unlock()
	if p.secret == nil {
		p.t.Errorf("unexpected secret prompt %+v", r)
		return SecretAnswer{}, errors.New("unexpected")
	}
	return p.secret(r, n)
}

func (p *fakePrompter) counts() (hosts, secrets int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.hosts), len(p.secrets)
}

func (p *fakePrompter) secretReqs() []SecretRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]SecretRequest(nil), p.secrets...)
}

func accept(context.Context, HostKeyRequest) (bool, error) { return true, nil }

func answer(v string, save bool) func(SecretRequest, int) (SecretAnswer, error) {
	return func(SecretRequest, int) (SecretAnswer, error) { return SecretAnswer{Value: v, Save: save}, nil }
}

type env struct {
	t     *testing.T
	home  string
	known string
	creds *config.Store
	ss    *sftptest.Server
	srv   config.Server
	p     *fakePrompter
}

func newEnv(t *testing.T, so sftptest.Options) *env {
	t.Helper()
	home := t.TempDir()
	if so.User == "" {
		so.User = "alice"
		so.Password = "secret"
	}
	ss := sftptest.Start(t, t.TempDir(), so)
	creds, err := config.Open(filepath.Join(home, "config.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := creds.SaveServers([]config.Server{{Name: "box", Host: ss.Host(), Port: ss.Port(), User: "alice",
		Auth: config.AuthPassword}}); err != nil {
		t.Fatal(err)
	}
	return &env{
		t: t, home: home, known: filepath.Join(home, ".ssh", "known_hosts"), creds: creds, ss: ss,
		srv: config.Server{Name: "box", Host: ss.Host(), Port: ss.Port(), User: "alice", Auth: config.AuthPassword},
		p:   &fakePrompter{t: t},
	}
}

func (e *env) manager(mod func(*Options)) *Manager {
	o := Options{
		Home: e.home, KnownHosts: e.known, Credentials: e.creds, Prompter: e.p,
		DialTimeout: 5 * time.Second,
		AgentConn:   func() (io.ReadWriteCloser, error) { return nil, errors.New("no agent in tests") },
	}
	if mod != nil {
		mod(&o)
	}
	m := NewManager(o)
	e.t.Cleanup(m.Close)
	return m
}

// ready trusts the host key and saves the right password, so dials never prompt.
func (e *env) ready() {
	e.t.Helper()
	e.trust(e.ss.KnownHostsLine())
	if err := e.creds.Set("box", config.Password, "secret"); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) trust(line string) {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Dir(e.known), 0o700); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(e.known, []byte(line+"\n"), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

func newKey(t *testing.T) (ed25519.PrivateKey, ssh.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return priv, sp
}

func otherKeyLine(t *testing.T, addr string) string {
	t.Helper()
	_, pub := newKey(t)
	return knownhosts.Line([]string{addr}, pub)
}

func mustKind(t *testing.T, err error, want errs.Kind) {
	t.Helper()
	if err == nil || errs.KindOf(err) != want {
		t.Fatalf("error = %v (kind %v), want kind %v", err, errs.KindOf(err), want)
	}
}

func mustStat(t *testing.T, c *Conn) {
	t.Helper()
	if _, err := c.SFTP.Stat(context.Background(), "/"); err != nil {
		t.Fatalf("Stat on connection: %v", err)
	}
}
