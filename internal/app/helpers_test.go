package app

import (
	"bytes"
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"spiid/internal/config"
	"spiid/internal/engine"
	"spiid/internal/sftptest"
	"spiid/internal/sshconn"
)

// scriptPrompter answers every prompt from fixed values, records the calls and fails the test if it is ever
// entered twice at once.
type scriptPrompter struct {
	t        *testing.T
	password string
	master   string
	inUse    atomic.Int32
	mu       sync.Mutex
	hosts    int
	secrets  []SecretRequest
}

func (p *scriptPrompter) enter() func() {
	if p.inUse.Add(1) > 1 {
		p.t.Errorf("Prompter entered twice at once")
	}
	time.Sleep(10 * time.Millisecond) // widen the window so an overlap would be seen
	return func() { p.inUse.Add(-1) }
}

func (p *scriptPrompter) ConfirmHostKey(context.Context, HostKeyRequest) (bool, error) {
	defer p.enter()()
	p.mu.Lock()
	p.hosts++
	p.mu.Unlock()
	return true, nil
}

func (p *scriptPrompter) Secret(_ context.Context, r SecretRequest) (SecretAnswer, error) {
	defer p.enter()()
	p.mu.Lock()
	p.secrets = append(p.secrets, r)
	p.mu.Unlock()
	if r.Kind == sshconn.SecretMaster {
		return SecretAnswer{Value: p.master}, nil
	}
	return SecretAnswer{Value: p.password}, nil
}

func (p *scriptPrompter) count(kind SecretKind) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, r := range p.secrets {
		if r.Kind == kind {
			n++
		}
	}
	return n
}

func (p *scriptPrompter) hostCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hosts
}

type env struct {
	t     *testing.T
	home  string
	cfg   string
	known string
	dl    string
	root  string
	ss    *sftptest.Server
	p     *scriptPrompter
}

func newEnv(t *testing.T, so sftptest.Options) *env {
	t.Helper()
	if so.User == "" {
		so.User, so.Password = "alice", "secret"
	}
	home := t.TempDir()
	e := &env{
		t: t, home: home, cfg: filepath.Join(home, "cfg"), known: filepath.Join(home, ".ssh", "known_hosts"),
		dl: filepath.Join(home, "dl"), root: t.TempDir(),
		p: &scriptPrompter{t: t, password: "secret", master: "master-password"},
	}
	e.ss = sftptest.Start(t, e.root, so)
	return e
}

// service opens a new Service on the env's folders (several may be opened one after the other).
func (e *env) service() *Service {
	e.t.Helper()
	s, err := New(Options{ConfigDir: e.cfg, Home: e.home, KnownHosts: e.known, Prompter: e.p})
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(s.Close)
	return s
}

// ready opens a service with the download folder and limits set.
func (e *env) ready(threads, parallel int) *Service {
	e.t.Helper()
	s := e.service()
	err := s.SaveSettings(
		config.Settings{DownloadDir: e.dl, ThreadsPerFile: threads, ParallelFiles: parallel, MaxConnections: 8})
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *env) server(auth config.AuthMethod) config.Server {
	return config.Server{Name: "box", Host: e.ss.Host(), Port: e.ss.Port(), User: "alice", Auth: auth}
}

func (e *env) trust() {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Dir(e.known), 0o700); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(e.known, []byte(e.ss.KnownHostsLine()+"\n"), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

// remote writes size pseudo-random bytes to root/rel and returns them.
func (e *env) remote(rel string, size int) []byte {
	e.t.Helper()
	data := make([]byte, size)
	rand.New(rand.NewSource(int64(size) + int64(len(rel)))).Read(data)
	p := filepath.Join(e.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		e.t.Fatal(err)
	}
	return data
}

func (e *env) mustHave(rel string, want []byte) {
	e.t.Helper()
	got, err := os.ReadFile(filepath.Join(e.dl, rel))
	if err != nil {
		e.t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		e.t.Fatalf("%s: content differs (%d bytes, want %d)", rel, len(got), len(want))
	}
}

func (e *env) readCreds() *config.Store {
	e.t.Helper()
	c, err := config.Open(filepath.Join(e.cfg, "config.json"), nil)
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

// settle waits until the service has n jobs and none is queued or running.
func settle(t *testing.T, s *Service, n int) []Job {
	t.Helper()
	return waitFor(t, s, "jobs to finish", func(js []Job) bool {
		if len(js) < n {
			return false
		}
		for _, j := range js {
			if j.State == engine.Queued || j.State == engine.Running {
				return false
			}
		}
		return true
	})
}

func waitFor(t *testing.T, s *Service, what string, ok func([]Job) bool) []Job {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		js := s.Jobs()
		if ok(js) {
			return js
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s; jobs: %+v", what, js)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func messageTexts(s *Service) []string {
	ms, _ := s.Messages(0)
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Text
	}
	return out
}

func sftpOpts() sftptest.Options { return sftptest.Options{} }

func waitTick() { time.Sleep(5 * time.Millisecond) }

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
