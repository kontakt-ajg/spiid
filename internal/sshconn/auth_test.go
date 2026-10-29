package sshconn

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"spiid/internal/config"
	"spiid/internal/errs"
	"spiid/internal/sftptest"
)

func get(t *testing.T, m *Manager, e *env) (*Conn, error) {
	t.Helper()
	return m.Pool(e.srv, 1).Get(context.Background())
}

func TestSavedPasswordRejected(t *testing.T) {
	e := newEnv(t, sftptest.Options{})
	e.trust(e.ss.KnownHostsLine())
	e.p.secret = func(r SecretRequest, n int) (SecretAnswer, error) {
		if !r.Rejected || !r.OfferSave || r.Retry || r.Kind != SecretPassword {
			t.Errorf("request = %+v", r)
		}
		return SecretAnswer{Value: "secret", Save: n == 1}, nil
	}
	if err := e.creds.Set("box", config.Password, "wrong"); err != nil {
		t.Fatal(err)
	}
	c, err := get(t, e.manager(nil), e)
	if err != nil {
		t.Fatal(err)
	}
	mustStat(t, c)
	if v, _, _ := e.creds.Get("box", config.Password); v != "secret" {
		t.Fatalf("credentials hold %q after Save", v)
	}
	// Second run: Save false leaves the (wrong) saved password alone.
	e.creds.Set("box", config.Password, "wrong")
	if c, err = get(t, e.manager(nil), e); err != nil {
		t.Fatal(err)
	}
	mustStat(t, c)
	if v, _, _ := e.creds.Get("box", config.Password); v != "wrong" {
		t.Fatalf("credentials changed to %q without Save", v)
	}
}

func TestAuthFailedForgetsSession(t *testing.T) {
	e := newEnv(t, sftptest.Options{NoPassword: true, User: "alice", Password: "secret"})
	e.trust(e.ss.KnownHostsLine())
	var right atomic.Bool
	e.p.secret = func(r SecretRequest, n int) (SecretAnswer, error) {
		if right.Load() {
			return SecretAnswer{Value: "secret"}, nil
		}
		return SecretAnswer{Value: "nope"}, nil
	}
	m := e.manager(nil)
	_, err := get(t, m, e)
	mustKind(t, err, errs.AuthFailed)
	if _, s := e.p.counts(); s != 3 {
		t.Fatalf("%d prompts for 3 wrong answers", s)
	}
	right.Store(true)
	c, err := get(t, m, e)
	if err != nil {
		t.Fatal(err)
	}
	mustStat(t, c)
	if _, s := e.p.counts(); s != 4 {
		t.Fatalf("%d prompts, want a new one (nothing wrong stayed cached)", s)
	}
	if last := e.p.secretReqs()[3]; last.Retry || last.Rejected {
		t.Fatalf("fresh dial asked with %+v", last)
	}
}

func TestForgetSecrets(t *testing.T) {
	e := newEnv(t, sftptest.Options{})
	e.trust(e.ss.KnownHostsLine())
	e.p.secret = answer("secret", false)
	m := e.manager(nil)
	p := m.Pool(e.srv, 1)
	for i, forget := range []bool{false, false, true} {
		if forget {
			m.ForgetSecrets("box")
		}
		c, err := p.Get(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		mustStat(t, c)
		p.Discard(c)
		_, s := e.p.counts()
		want := 1
		if forget {
			want = 2
		}
		if s != want {
			t.Fatalf("round %d: %d prompts, want %d", i, s, want)
		}
	}
}

func TestKeyboardInteractiveOnly(t *testing.T) {
	e := newEnv(t, sftptest.Options{NoPassword: true, User: "alice", Password: "secret"})
	e.ready()
	c, err := get(t, e.manager(nil), e)
	if err != nil {
		t.Fatal(err)
	}
	mustStat(t, c)
	if _, s := e.p.counts(); s != 0 {
		t.Fatalf("%d prompts, the saved password should have been used", s)
	}
}

func TestAgentAuth(t *testing.T) {
	priv, pub := newKey(t)
	e := newEnv(t, sftptest.Options{User: "alice", Password: "unused", AuthorizedKeys: []ssh.PublicKey{pub}})
	e.trust(e.ss.KnownHostsLine())
	kr := agent.NewKeyring()
	if err := kr.Add(agent.AddedKey{PrivateKey: priv}); err != nil {
		t.Fatal(err)
	}
	e.srv.Auth = config.AuthAgent
	m := e.manager(func(o *Options) {
		o.AgentConn = func() (io.ReadWriteCloser, error) {
			a, b := net.Pipe()
			go agent.ServeAgent(kr, b)
			return a, nil
		}
	})
	c, err := get(t, m, e)
	if err != nil {
		t.Fatal(err)
	}
	mustStat(t, c)
}

func TestAgentMissing(t *testing.T) {
	e := newEnv(t, sftptest.Options{})
	e.trust(e.ss.KnownHostsLine())
	e.srv.Auth = config.AuthAgent
	m := e.manager(func(o *Options) {
		o.AgentConn = func() (io.ReadWriteCloser, error) { return nil, errors.New("no socket") }
	})
	_, err := get(t, m, e)
	mustKind(t, err, errs.AuthFailed)
	if n := e.ss.Connections(); n != 0 {
		t.Fatalf("%d connections made", n)
	}
}

func TestAgentReadHonorsDeadline(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout time.Duration
		cancel  bool
		kind    errs.Kind
	}{
		{"timeout", 100 * time.Millisecond, false, errs.ConnectionLost},
		{"cancellation", 10 * time.Second, true, errs.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, pub := newKey(t)
			e := newEnv(t, sftptest.Options{AuthorizedKeys: []ssh.PublicKey{pub}})
			e.trust(e.ss.KnownHostsLine())
			e.srv.Auth = config.AuthAgent
			a, b := net.Pipe()
			defer a.Close()
			defer b.Close()
			drained := make(chan struct{})
			reading := make(chan struct{})
			go func() {
				buf := make([]byte, 4096)
				b.Read(buf)
				close(reading)
				io.Copy(io.Discard, b)
				close(drained)
			}()
			m := e.manager(func(o *Options) {
				o.DialTimeout = tc.timeout
				o.AgentConn = func() (io.ReadWriteCloser, error) { return a, nil }
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ch := make(chan error, 1)
			go func() {
				c, err := m.Pool(e.srv, 1).Get(ctx)
				if c != nil {
					c.close()
				}
				ch <- err
			}()
			if tc.cancel {
				<-reading
				cancel()
			}
			select {
			case err := <-ch:
				if errs.KindOf(err) != tc.kind {
					t.Errorf("got %v", err)
				}
			case <-time.After(time.Second):
				t.Error("Get still blocked: agent read is not interrupted")
				b.Close()
				<-ch
			}
			a.Close()
			b.Close()
			<-drained
		})
	}
}

func TestPasswordSaveFailureWarnsWithoutFailingLogin(t *testing.T) {
	e := newEnv(t, sftptest.Options{})
	e.trust(e.ss.KnownHostsLine())
	e.p.secret = answer("secret", true)
	// The Store has opened a missing file. Make its eventual destination unwritable as a file.
	os.Remove(filepath.Join(e.home, "config.json"))
	if err := os.Mkdir(filepath.Join(e.home, "config.json"), 0700); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var warns []string
	m := e.manager(func(o *Options) {
		o.Warn = func(msg string) {
			mu.Lock()
			warns = append(warns, msg)
			mu.Unlock()
		}
	})
	p := m.Pool(e.srv, 1)
	c, err := p.Get(context.Background())
	if err != nil {
		t.Fatalf("Get must succeed: %v", err)
	}
	defer p.Discard(c)
	mu.Lock()
	defer mu.Unlock()
	if len(warns) != 1 || !strings.Contains(warns[0], "could not save the password") {
		t.Fatalf("warnings = %q", warns)
	}
	if _, ok, _ := e.creds.Get("box", "password"); ok {
		t.Error("password was saved")
	}
}

func TestAgentTimeoutResumesAfterPrompt(t *testing.T) {
	priv, pub := newKey(t)
	e := newEnv(t, sftptest.Options{AuthorizedKeys: []ssh.PublicKey{pub}})
	e.srv.Auth = config.AuthAgent
	e.p.host = func(context.Context, HostKeyRequest) (bool, error) {
		time.Sleep(400 * time.Millisecond)
		return true, nil
	}
	kr := agent.NewKeyring()
	if err := kr.Add(agent.AddedKey{PrivateKey: priv}); err != nil {
		t.Fatal(err)
	}
	a, b := net.Pipe()
	done := make(chan struct{})
	go func() { defer close(done); agent.ServeAgent(kr, b) }()
	defer func() { a.Close(); b.Close(); <-done }()
	m := e.manager(func(o *Options) {
		o.DialTimeout = 200 * time.Millisecond
		o.AgentConn = func() (io.ReadWriteCloser, error) { return a, nil }
	})
	p := m.Pool(e.srv, 1)
	c, err := p.Get(context.Background())
	if err != nil {
		t.Fatalf("responsive agent and accepted prompt: %v", err)
	}
	p.Discard(c)
}

func TestParallelRejectedPasswordsPromptOnce(t *testing.T) {
	srv, m, s, p, store := probeSetup(t, sftptest.Options{User: "u", Password: "right"}, 0)
	store.Set("s", config.Password, "wrong")
	pool := m.Pool(s, 6)
	release := make(chan struct{})
	p.entered = make(chan struct{})
	p.release = release
	started := make(chan struct{}, 6)
	var wg sync.WaitGroup
	errc := make(chan error, 6)
	conns := make(chan *Conn, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			started <- struct{}{}
			c, err := pool.Get(context.Background())
			errc <- err
			if c != nil {
				conns <- c
			}
		}()
	}
	for i := 0; i < 6; i++ {
		<-started
	}
	<-p.entered
	close(release)
	wg.Wait()
	close(errc)
	close(conns)
	for err := range errc {
		if err != nil {
			t.Fatal(err)
		}
	}
	for c := range conns {
		pool.Put(c)
	}
	if p.secrets != 1 {
		t.Fatalf("want exactly 1 password prompt for 6 parallel dials with a wrong saved password, got %d (%+v)",
			p.secrets, p.reqs)
	}
	if !p.reqs[0].Rejected || !p.reqs[0].OfferSave {
		t.Errorf("first prompt should be Rejected+OfferSave: %+v", p.reqs[0])
	}
	_ = srv
}
