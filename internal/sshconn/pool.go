package sshconn

import (
	"context"
	"sync"

	"spiid/internal/config"
	"spiid/internal/errs"
)

// Pool holds the connections to one server.
type Pool struct {
	m   *Manager
	srv config.Server

	mu      sync.Mutex
	max     int
	open    int
	idle    []*Conn
	waiters []chan struct{}
	closed  bool
}

func newPool(m *Manager, srv config.Server, max int) *Pool {
	return &Pool{m: m, srv: srv, max: max}
}

func (p *Pool) setMax(n int) {
	p.mu.Lock()
	p.max = n
	for len(p.waiters) > 0 {
		p.wakeOne()
	}
	p.mu.Unlock()
}

// wakeOne wakes the longest waiting Get; the caller holds p.mu.
func (p *Pool) wakeOne() {
	if len(p.waiters) > 0 {
		close(p.waiters[0])
		p.waiters = p.waiters[1:]
	}
}

func (p *Pool) removeWaiter(w chan struct{}) bool {
	for i, x := range p.waiters {
		if x == w {
			p.waiters = append(p.waiters[:i], p.waiters[i+1:]...)
			return true
		}
	}
	return false
}

// Get returns an idle connection, dials a new one below the limit, or waits for a Put/Discard.
func (p *Pool) Get(ctx context.Context) (*Conn, error) {
	p.mu.Lock()
	for {
		if p.closed {
			name := p.srv.Name
			p.mu.Unlock()
			return nil, errs.New(errs.PoolClosed, "connect", name, nil)
		}
		for len(p.idle) > 0 {
			c := p.idle[len(p.idle)-1]
			p.idle = p.idle[:len(p.idle)-1]
			if c.dead() {
				c.close()
				p.open--
				continue
			}
			c.returned.Store(false)
			p.mu.Unlock()
			return c, nil
		}
		if p.open < p.max {
			p.open++
			srv := p.srv
			p.mu.Unlock()
			c, err := p.m.dial(ctx, srv)
			if err != nil {
				p.mu.Lock()
				p.open--
				p.wakeOne()
				p.mu.Unlock()
				return nil, err
			}
			return c, nil
		}
		w := make(chan struct{})
		p.waiters = append(p.waiters, w)
		p.mu.Unlock()
		select {
		case <-w:
			p.mu.Lock()
		case <-ctx.Done():
			p.mu.Lock()
			if !p.removeWaiter(w) {
				p.wakeOne()
			}
			name := p.srv.Name
			p.mu.Unlock()
			return nil, errs.New(errs.Canceled, "connect", name, ctx.Err())
		}
	}
}

// Put returns a healthy connection; a second Put/Discard of the same connection is a no-op.
func (p *Pool) Put(c *Conn) {
	if !c.returned.CompareAndSwap(false, true) {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || c.dead() {
		c.close()
		p.open--
	} else {
		p.idle = append(p.idle, c)
	}
	p.wakeOne()
}

// Discard closes a broken connection and frees its slot; a second Put/Discard is a no-op.
func (p *Pool) Discard(c *Conn) {
	if !c.returned.CompareAndSwap(false, true) {
		return
	}
	c.close()
	p.mu.Lock()
	p.open--
	p.wakeOne()
	p.mu.Unlock()
}

// Close closes idle connections; waiters get errs.PoolClosed and connections in use are closed on Put.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for _, c := range p.idle {
		c.close()
		p.open--
	}
	p.idle = nil
	for _, w := range p.waiters {
		close(w)
	}
	p.waiters = nil
}
