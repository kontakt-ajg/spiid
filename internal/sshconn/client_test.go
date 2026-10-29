package sshconn

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"spiid/internal/config"
	"spiid/internal/sftptest"
)

type probePrompter struct {
	mu       sync.Mutex
	inside   int32
	secrets  int32
	hostkeys int32
	answer   string
	reqs     []SecretRequest
	t        *testing.T
	entered  chan struct{}
	enterOne sync.Once
	release  <-chan struct{}
}

func (p *probePrompter) ConfirmHostKey(ctx context.Context, r HostKeyRequest) (bool, error) {
	if atomic.AddInt32(&p.inside, 1) != 1 {
		p.t.Error("prompter entered twice")
	}
	defer atomic.AddInt32(&p.inside, -1)
	atomic.AddInt32(&p.hostkeys, 1)
	return true, nil
}

func (p *probePrompter) Secret(ctx context.Context, r SecretRequest) (SecretAnswer, error) {
	if atomic.AddInt32(&p.inside, 1) != 1 {
		p.t.Error("prompter entered twice")
	}
	defer atomic.AddInt32(&p.inside, -1)
	atomic.AddInt32(&p.secrets, 1)
	p.mu.Lock()
	p.reqs = append(p.reqs, r)
	p.mu.Unlock()
	if p.entered != nil {
		p.enterOne.Do(func() { close(p.entered) }) // a second prompt fails the test above, not with a panic
	}
	if p.release != nil {
		select {
		case <-p.release:
		case <-ctx.Done():
			return SecretAnswer{}, ctx.Err()
		}
	}
	return SecretAnswer{Value: p.answer}, nil
}

func probeSetup(
	t *testing.T, opts sftptest.Options, keepalive time.Duration,
) (*sftptest.Server, *Manager, config.Server, *probePrompter, *config.Store) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "f"), []byte("x"), 0o644)
	srv := sftptest.Start(t, root, opts)
	home := t.TempDir()
	kh := filepath.Join(home, "known_hosts")
	os.WriteFile(kh, []byte(srv.KnownHostsLine()+"\n"), 0o600)
	store, err := config.Open(filepath.Join(home, "config.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	p := &probePrompter{t: t, answer: opts.Password}
	m := NewManager(Options{Home: home, KnownHosts: kh, Credentials: store, Prompter: p,
		Keepalive: keepalive, KeepaliveMiss: 3,
		AgentConn: func() (io.ReadWriteCloser, error) { return nil, errors.New("no agent") }})
	t.Cleanup(m.Close)
	s := config.Server{
		Name: "s",
		Host: srv.Host(),
		Port: srv.Port(),
		User: opts.User,
		Auth: config.AuthPassword,
	}
	if err := store.SaveServers([]config.Server{s}); err != nil {
		t.Fatal(err)
	}
	return srv, m, s, p, store
}
