package sshconn

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh/knownhosts"
	"spiid/internal/sftptest"
)

// stallProxy forwards TCP to addr until stall is set; then it reads and drops everything without closing.
type stallProxy struct {
	ln        net.Listener
	stall     atomic.Bool
	forwarded atomic.Int64
}

func newStallProxy(t *testing.T, addr string) *stallProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &stallProxy{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			up, err := net.Dial("tcp", addr)
			if err != nil {
				c.Close()
				continue
			}
			t.Cleanup(func() { c.Close(); up.Close() })
			go p.pipe(c, up)
			go p.pipe(up, c)
		}
	}()
	return p
}

func (p *stallProxy) pipe(dst, src net.Conn) {
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 && !p.stall.Load() {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
			p.forwarded.Add(1)
		}
		if err != nil {
			return
		}
	}
}

func TestKeepaliveDetectsDeadConnection(t *testing.T) {
	e := newEnv(t, sftptest.Options{})
	e.ready()
	px := newStallProxy(t, net.JoinHostPort(e.ss.Host(), fmt.Sprint(e.ss.Port())))
	e.srv.Port = px.ln.Addr().(*net.TCPAddr).Port
	e.trust(knownhosts.Line([]string{fmt.Sprintf("[127.0.0.1]:%d", e.srv.Port)}, e.ss.HostKey()))
	m := e.manager(func(o *Options) {
		o.Keepalive = 50 * time.Millisecond
		o.KeepaliveMiss = 3
	})
	c, err := get(t, m, e)
	if err != nil {
		t.Fatal(err)
	}
	mustStat(t, c)
	select {
	case <-c.SFTP.Done():
		t.Fatal("connection already closed before the stall")
	default:
	}
	px.stall.Store(true)
	select {
	case <-c.SFTP.Done():
	case <-time.After(time.Second):
		t.Fatal("dead connection was not detected within 1s")
	}
}

func TestKeepaliveFailureReplyCountsAsAlive(t *testing.T) {
	e := newEnv(t, sftptest.Options{})
	e.ready()
	px := newStallProxy(t, net.JoinHostPort(e.ss.Host(), fmt.Sprint(e.ss.Port())))
	e.srv.Port = px.ln.Addr().(*net.TCPAddr).Port
	e.trust(knownhosts.Line([]string{fmt.Sprintf("[127.0.0.1]:%d", e.srv.Port)}, e.ss.HostKey()))
	m := e.manager(func(o *Options) { o.Keepalive = 30 * time.Millisecond; o.KeepaliveMiss = 3 })
	pool := m.Pool(e.srv, 1)
	c, err := pool.Get(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	// With no SFTP operations in flight, traffic is the keepalive requests and replies.
	baseline := px.forwarded.Load()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	deadline := time.After(2 * time.Second)
	for px.forwarded.Load() < baseline+8 {
		select {
		case <-c.SFTP.Done():
			t.Fatal("connection closed while waiting for keepalive replies")
		case <-deadline:
			t.Fatal("keepalive requests and replies were not observed")
		case <-tick.C:
		}
	}

	select {
	case <-c.SFTP.Done():
		t.Fatal("connection closed although the server answers keepalives (with failure replies)")
	default:
	}
	if _, err := c.SFTP.Stat(context.Background(), "/"); err != nil {
		t.Fatal(err)
	}
	pool.Put(c)
}
