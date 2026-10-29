package sshconn

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"spiid/internal/config"
	"spiid/internal/errs"
	"spiid/internal/sftp"
)

// gate is a dial deadline that can be paused while the user is being asked something. A nil gate does nothing.
type gate struct {
	mu        sync.Mutex
	conn      net.Conn
	deadline  time.Time
	remaining time.Duration
	depth     int
}

func newGate(timeout time.Duration) *gate {
	return &gate{deadline: time.Now().Add(timeout)}
}

func (g *gate) attach(c net.Conn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.conn = c
	if g.depth == 0 {
		c.SetDeadline(g.deadline)
	}
}

func (g *gate) pause() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.depth++
	if g.depth == 1 {
		g.remaining = time.Until(g.deadline)
		if g.conn != nil {
			g.conn.SetDeadline(time.Time{})
		}
	}
}

func (g *gate) resume() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.depth--
	if g.depth == 0 {
		g.deadline = time.Now().Add(g.remaining)
		if g.conn != nil {
			g.conn.SetDeadline(g.deadline)
		}
	}
}

// dial opens an SSH connection to srv and starts the "sftp" subsystem.
func (m *Manager) dial(ctx context.Context, srv config.Server) (result *Conn, dialErr error) {
	defer func() {
		if errs.KindOf(dialErr) == errs.AuthFailed {
			m.opts.Log.Warn("ssh: login failed for %s", srv.Name)
		}
	}()
	l := &login{m: m, ctx: ctx, srv: srv}
	defer l.cleanup()
	methods, err := l.authMethods()
	if err != nil {
		if errs.KindOf(err) == errs.AuthFailed {
			m.ForgetSecrets(srv.Name)
		}
		return nil, err
	}
	l.gate = newGate(m.opts.DialTimeout)
	dctx, cancel := context.WithDeadline(ctx, l.gate.deadline)
	nc, err := m.opts.DialContext(dctx, "tcp", srv.Addr())
	cancel()
	if err != nil {
		return nil, l.classify(err)
	}
	l.gate.attach(nc)
	stop := context.AfterFunc(ctx, func() { nc.Close() })
	conn, err := l.open(nc, methods)
	if err != nil {
		nc.Close()
		stop()
		return nil, l.classify(err)
	}
	if !stop() {
		conn.close()
		return nil, errs.New(errs.Canceled, "dial", srv.Name, ctx.Err())
	}
	m.opts.Log.Info("ssh: connected to %s (%s@%s:%d)", srv.Name, srv.User, srv.Host, srv.Port)
	l.commit()
	return conn, nil
}

func (l *login) open(nc net.Conn, methods []ssh.AuthMethod) (*Conn, error) {
	addr := l.srv.Addr()
	cfg := &ssh.ClientConfig{
		User:              l.srv.User,
		Auth:              methods,
		HostKeyCallback:   l.hostKeyCallback,
		HostKeyAlgorithms: hostKeyAlgorithms(l.m.knownKeyTypes(addr, nc.RemoteAddr())),
	}
	sc, chans, reqs, err := ssh.NewClientConn(nc, addr, cfg)
	if err != nil {
		return nil, err
	}
	l.cleanup()
	client := ssh.NewClient(sc, chans, reqs)
	sess, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, err
	}
	stdin, err := sess.StdinPipe()
	if err == nil {
		var stdout io.Reader
		stdout, err = sess.StdoutPipe()
		if err == nil {
			err = sess.RequestSubsystem("sftp")
		}
		if err == nil {
			var sc *sftp.Client
			sc, err = sftp.NewClient(stdout, stdin)
			if err == nil {
				nc.SetDeadline(time.Time{})
				return l.m.newConn(sc, client, l.srv.Name), nil
			}
		}
	}
	client.Close()
	return nil, err
}

// classify turns a dial or handshake error into an errs error.
func (l *login) classify(err error) error {
	if e := l.firstErr(); e != nil {
		if errs.KindOf(e) == errs.AuthFailed {
			l.m.ForgetSecrets(l.srv.Name)
		}
		return e
	}
	if l.ctx.Err() != nil {
		return errs.New(errs.Canceled, "dial", l.srv.Name, l.ctx.Err())
	}
	if l.agentStalled.Load() {
		return errs.New(errs.ConnectionLost, "agent", l.srv.Name, errors.New("ssh-agent did not answer"))
	}
	var ne net.Error
	if errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return errs.New(errs.ConnectionLost, "dial", l.srv.Name, errors.New("timeout"))
	}
	if strings.Contains(err.Error(), "unable to authenticate") {
		l.m.ForgetSecrets(l.srv.Name)
		return errs.New(errs.AuthFailed, "login", l.srv.Name, err)
	}
	return errs.New(errs.ConnectionLost, "dial", l.srv.Name, err)
}
