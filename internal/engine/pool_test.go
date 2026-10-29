package engine

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"spiid/internal/errs"
	"spiid/internal/sftp"
	"spiid/internal/sftptest"
	"spiid/internal/sshconn"
)

type rwc struct {
	io.Reader
	io.Writer
	closers []io.Closer
}

func (r rwc) Close() error {
	for _, c := range r.closers {
		c.Close()
	}
	return nil
}

// tpool is a test Pool over in-memory SFTP servers. It checks that every Get is followed by exactly one
// Put or Discard.
type tpool struct {
	t       *testing.T
	root    string
	delay   atomic.Int64 // read delay in ns
	gets    atomic.Int32
	maxRead atomic.Int64 // 0 = no cap

	mu        sync.Mutex
	failGet   error
	beforeGet func(int32)
	conns     map[*sshconn.Conn]*connInfo
	idle      []*sshconn.Conn
}

type connInfo struct {
	out        bool
	dead       bool
	puts, dscs int
}

func newPool(t *testing.T, root string) *tpool {
	return &tpool{t: t, root: root, conns: map[*sshconn.Conn]*connInfo{}}
}

func (p *tpool) setDelay(d time.Duration) { p.delay.Store(int64(d)) }

func (p *tpool) setFail(err error) {
	p.mu.Lock()
	p.failGet = err
	p.mu.Unlock()
}

func (p *tpool) Get(ctx context.Context) (*sshconn.Conn, error) {
	n := p.gets.Add(1)
	if p.beforeGet != nil {
		p.beforeGet(n)
	}
	if err := ctx.Err(); err != nil {
		return nil, errs.New(errs.Canceled, "connect", "", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failGet != nil {
		return nil, p.failGet
	}
	for len(p.idle) > 0 {
		c := p.idle[len(p.idle)-1]
		p.idle = p.idle[:len(p.idle)-1]
		select {
		case <-c.SFTP.Done():
			continue
		default:
		}
		p.conns[c].out = true
		return c, nil
	}
	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	server := rwc{Reader: c2sR, Writer: s2cW, closers: []io.Closer{c2sR, s2cW}}
	opts := sftptest.ServeOptions{
		ReadDelay: func() time.Duration { return time.Duration(p.delay.Load()) },
		MaxRead:   int(p.maxRead.Load()),
	}
	go sftptest.ServeSFTP(server, p.root, opts)
	cl, err := sftp.NewClient(s2cR, c2sW)
	if err != nil {
		p.t.Errorf("test pool: %v", err)
		return nil, err
	}
	c := &sshconn.Conn{SFTP: cl}
	p.conns[c] = &connInfo{out: true}
	return c, nil
}

func (p *tpool) back(c *sshconn.Conn, discard bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ci := p.conns[c]
	if ci == nil || !ci.out || ci.dead {
		p.t.Errorf("connection returned when not checked out (put=%v)", !discard)
		return
	}
	ci.out = false
	if discard {
		ci.dscs++
		ci.dead = true
		c.SFTP.Close()
		return
	}
	ci.puts++
	p.idle = append(p.idle, c)
}

func (p *tpool) Put(c *sshconn.Conn) { p.back(c, false) }

func (p *tpool) Discard(c *sshconn.Conn) { p.back(c, true) }

// dropAll closes every live client, like a network failure.
func (p *tpool) dropAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for c, ci := range p.conns {
		if !ci.dead {
			c.SFTP.Close()
		}
	}
}

// verify fails if a connection is still checked out; returns total puts+discards.
func (p *tpool) verify() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, ci := range p.conns {
		if ci.out {
			p.t.Errorf("connection never returned")
		}
		n += ci.puts + ci.dscs
	}
	if int(p.gets.Load()) < n {
		p.t.Errorf("more returns (%d) than gets (%d)", n, p.gets.Load())
	}
	return n
}
