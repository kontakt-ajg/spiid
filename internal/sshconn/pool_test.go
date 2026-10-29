package sshconn

import (
	"context"
	"testing"
	"time"

	"spiid/internal/config"
	"spiid/internal/errs"
	"spiid/internal/sftptest"
)

func readyPool(t *testing.T, max int) (*env, *Manager, *Pool) {
	e := newEnv(t, sftptest.Options{})
	e.ready()
	m := e.manager(nil)
	return e, m, m.Pool(e.srv, max)
}

type getResult struct {
	c   *Conn
	err error
}

func asyncGet(p *Pool) chan getResult {
	ch := make(chan getResult, 1)
	go func() {
		c, err := p.Get(context.Background())
		ch <- getResult{c, err}
	}()
	return ch
}

func mustBlock(t *testing.T, ch chan getResult) {
	t.Helper()
	select {
	case r := <-ch:
		t.Fatalf("Get returned early: %v", r.err)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestPoolReuseAndLimit(t *testing.T) {
	e, _, p := readyPool(t, 2)
	a, err := p.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	third := asyncGet(p)
	mustBlock(t, third)
	p.Put(a)
	r := <-third
	if r.err != nil || r.c != a {
		t.Fatalf("third Get = %v, %v; want the returned connection", r.c, r.err)
	}
	mustStat(t, r.c)
	p.Put(b)
	if n := e.ss.Connections(); n != 2 {
		t.Fatalf("%d connections, want 2", n)
	}
}

func TestPoolPutTwice(t *testing.T) {
	_, _, p := readyPool(t, 2)
	c, err := p.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p.Put(c)
	p.Put(c)
	p.Discard(c)
	if openCount(p) != 1 || len(p.idle) != 1 {
		t.Fatalf("Open = %d, idle = %d; want 1 and 1", openCount(p), len(p.idle))
	}
	c2, err := p.Get(context.Background())
	if err != nil || c2 != c {
		t.Fatalf("Get = %v, %v", c2, err)
	}
	p.Discard(c2)
	p.Discard(c2)
	p.Put(c2)
	if openCount(p) != 0 {
		t.Fatalf("Open = %d, want 0", openCount(p))
	}
}

func TestPoolClosed(t *testing.T) {
	_, _, p := readyPool(t, 1)
	c, err := p.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	waiter := asyncGet(p)
	mustBlock(t, waiter)
	p.Close()
	r := <-waiter
	mustKind(t, r.err, errs.PoolClosed)
	_, err = p.Get(context.Background())
	mustKind(t, err, errs.PoolClosed)
	p.Put(c)
	if openCount(p) != 0 {
		t.Fatalf("Open = %d after Put on a closed pool", openCount(p))
	}
}

func TestPoolSkipsDeadIdle(t *testing.T) {
	e, _, p := readyPool(t, 1)
	c, err := p.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	mustStat(t, c)
	p.Put(c)
	e.ss.DropAll()
	select {
	case <-c.SFTP.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("dropped connection never reported Done")
	}
	c2, err := p.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if c2 == c {
		t.Fatal("got the dead connection back")
	}
	mustStat(t, c2)
	if openCount(p) != 1 {
		t.Fatalf("Open = %d, want 1", openCount(p))
	}
}

func TestManagerPoolReplace(t *testing.T) {
	e, m, p1 := readyPool(t, 2)
	if p2 := m.Pool(e.srv, 5); p2 != p1 || p1.max != 5 {
		t.Fatalf("same server must give the same pool with the new limit (max %d)", p1.max)
	}
	c, err := p1.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	changed := e.srv
	changed.StartDir = "/elsewhere"
	if m.Pool(changed, 2) != p1 {
		t.Fatal("StartDir edit replaced pool")
	}
	changed.Name = "renamed"
	m.RenamePool(e.srv.Name, changed.Name)
	if m.Pool(changed, 2) != p1 {
		t.Fatal("rename replaced pool")
	}
	mustStat(t, c)
	if err := e.creds.EditServers([]config.Server{changed}, e.srv.Name, changed.Name, nil); err != nil {
		t.Fatal(err)
	}
	p1.Discard(c)
	c, err = p1.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	mustStat(t, c)
	p1.Put(c)
	changed.Host = "different.example"
	p3 := m.Pool(changed, 2)
	if p3 == p1 {
		t.Fatal("Host edit must replace pool")
	}
	_, err = p1.Get(context.Background())
	mustKind(t, err, errs.PoolClosed)

}

func TestRaisingPoolLimitWakesAllWaiters(t *testing.T) {
	e, m, p := readyPool(t, 1)
	c, err := p.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Discard(c)
	a, b := asyncGet(p), asyncGet(p)
	deadline := time.Now().Add(time.Second)
	for {
		p.mu.Lock()
		n := len(p.waiters)
		p.mu.Unlock()
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("waiters not registered")
		}
		time.Sleep(time.Millisecond)
	}
	m.Pool(e.srv, 3)
	defer p.Close()
	for i, ch := range []chan getResult{a, b} {
		select {
		case r := <-ch:
			if r.err != nil {
				t.Fatal(r.err)
			}
			defer p.Discard(r.c)
		case <-time.After(300 * time.Millisecond):
			t.Errorf("waiter %d still blocked: Open=%d max=3", i, openCount(p))
			p.Close()
			r := <-ch
			if r.c != nil {
				p.Discard(r.c)
			}
		}
	}
}

func TestPoolWaitHonorsCancellation(t *testing.T) {
	_, m, s, _, store := probeSetup(t, sftptest.Options{User: "u", Password: "pw"}, 0)
	store.Set("s", config.Password, "pw")
	pool := m.Pool(s, 1)
	c, err := pool.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = pool.Get(ctx)
	if errs.KindOf(err) != errs.Canceled || time.Since(start) > time.Second {
		t.Fatalf("waiting Get: %v after %v", err, time.Since(start))
	}
	pool.Put(c)
	if openCount(pool) != 1 {
		t.Fatalf("open = %d", openCount(pool))
	}
}

func TestRenamePoolClosesPoolCreatedUnderNewName(t *testing.T) {
	e, m, old := readyPool(t, 2)
	renamed := e.srv
	renamed.Name = "renamed"
	early := m.Pool(renamed, 2) // a request that saw the new name before the rename finished
	m.RenamePool(e.srv.Name, renamed.Name)
	if m.Pool(renamed, 2) != old {
		t.Fatal("the renamed pool must keep its connections")
	}
	early.mu.Lock()
	closed := early.closed
	early.mu.Unlock()
	if !closed {
		t.Fatal("the pool created under the new name before the rename was not closed")
	}
}

func openCount(p *Pool) int { p.mu.Lock(); defer p.mu.Unlock(); return p.open }
