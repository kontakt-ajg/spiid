package sftptest

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Options configures Start.
type Options struct {
	User, Password string          // password auth (and keyboard-interactive with the same password)
	ReadDelay      time.Duration   // sleep before every READ reply
	MaxRead        int             // as ServeOptions.MaxRead
	AuthorizedKeys []ssh.PublicKey // public-key auth (agent / key file tests)
	NoPassword     bool            // disable the "password" method (keyboard-interactive still works)
}

// Server is an SSH server with an "sftp" subsystem.
type Server struct {
	root    string
	opts    Options
	ln      net.Listener
	hostKey ssh.PublicKey
	delay   atomic.Int64
	count   atomic.Int64
	wg      sync.WaitGroup

	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	closed bool
}

// Start runs an SSH server on 127.0.0.1:0 with an ed25519 host key; t.Cleanup closes it.
func Start(t interface {
	Helper()
	Fatalf(string, ...any)
	Cleanup(func())
}, root string, opts Options) *Server {
	t.Helper()
	s, err := Serve(root, opts, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Cleanup(s.Close)
	return s
}

// Serve runs an SSH server on addr (a TCP listen address) with an ed25519 host key. The caller must call Close.
func Serve(root string, opts Options, addr string) (*Server, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("sftptest: host key: %w", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, fmt.Errorf("sftptest: signer: %w", err)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("sftptest: listen: %w", err)
	}
	s := &Server{root: root, opts: opts, ln: ln, hostKey: signer.PublicKey(), conns: map[net.Conn]struct{}{}}
	s.delay.Store(int64(opts.ReadDelay))
	cfg := &ssh.ServerConfig{
		KeyboardInteractiveCallback: func(
			c ssh.ConnMetadata, ask ssh.KeyboardInteractiveChallenge,
		) (*ssh.Permissions, error) {
			answers, err := ask(c.User(), "", []string{"Password: "}, []bool{false})
			if err != nil {
				return nil, err
			}
			if len(answers) != 1 {
				return nil, errors.New("expected one answer")
			}
			return nil, s.check(c.User(), answers[0])
		},
	}
	if !opts.NoPassword {
		cfg.PasswordCallback = func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			return nil, s.check(c.User(), string(pw))
		}
	}
	if len(opts.AuthorizedKeys) > 0 {
		cfg.PublicKeyCallback = func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			for _, k := range opts.AuthorizedKeys {
				if bytes.Equal(k.Marshal(), key.Marshal()) {
					return nil, nil
				}
			}
			return nil, errors.New("key not authorized")
		}
	}
	cfg.AddHostKey(signer)
	s.wg.Add(1)
	go s.acceptLoop(cfg)
	return s, nil
}

func (s *Server) check(user, password string) error {
	if user == s.opts.User && password == s.opts.Password {
		return nil
	}
	return errors.New("wrong user or password")
}

// Host returns the listen address host.
func (s *Server) Host() string {
	return s.ln.Addr().(*net.TCPAddr).IP.String()
}

// Port returns the listen port.
func (s *Server) Port() int {
	return s.ln.Addr().(*net.TCPAddr).Port
}

// HostKey returns the server's public host key.
func (s *Server) HostKey() ssh.PublicKey {
	return s.hostKey
}

// KnownHostsLine returns a known_hosts line for the server: "[127.0.0.1]:port ssh-ed25519 AAAA…".
func (s *Server) KnownHostsLine() string {
	return knownhosts.Line([]string{fmt.Sprintf("[%s]:%d", s.Host(), s.Port())}, s.hostKey)
}

// SetReadDelay changes the sleep before every READ reply; safe while connections run.
func (s *Server) SetReadDelay(d time.Duration) {
	s.delay.Store(int64(d))
}

// Connections returns the number of connections accepted so far.
func (s *Server) Connections() int {
	return int(s.count.Load())
}

// DropAll closes every open connection now.
func (s *Server) DropAll() {
	s.mu.Lock()
	for c := range s.conns {
		c.Close()
	}
	s.mu.Unlock()
}

// Close stops the listener and drops all connections.
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.ln.Close()
	s.DropAll()
	s.wg.Wait()
}

func (s *Server) track(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.conns[c] = struct{}{}
	return true
}

func (s *Server) untrack(c net.Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
	c.Close()
}

func (s *Server) acceptLoop(cfg *ssh.ServerConfig) {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.count.Add(1)
		if !s.track(c) {
			c.Close()
			continue
		}
		s.wg.Add(1)
		go s.serveConn(c, cfg)
	}
}

func (s *Server) serveConn(c net.Conn, cfg *ssh.ServerConfig) {
	defer s.wg.Done()
	defer s.untrack(c)
	sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	defer sc.Close()
	done := make(chan struct{})
	go func() {
		sc.Wait()
		close(done)
	}()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "only session channels")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			continue
		}
		s.wg.Add(1)
		go s.serveSession(ch, creqs, done)
	}
}

func (s *Server) serveSession(ch ssh.Channel, reqs <-chan *ssh.Request, done <-chan struct{}) {
	defer s.wg.Done()
	for r := range reqs {
		if r.Type != "subsystem" || subsystemName(r.Payload) != "sftp" {
			if r.WantReply {
				r.Reply(false, nil)
			}
			continue
		}
		r.Reply(true, nil)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			ServeSFTP(ch, s.root, ServeOptions{
				ReadDelay: func() time.Duration { return time.Duration(s.delay.Load()) },
				MaxRead:   s.opts.MaxRead,
				Done:      done,
			})
			ch.Close()
		}()
	}
}

func subsystemName(payload []byte) string {
	d := &dec{b: payload}
	return d.str()
}
