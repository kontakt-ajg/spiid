package sshconn

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"spiid/internal/errs"
	"spiid/internal/sftptest"
)

func TestPasswordPromptOnceParallel(t *testing.T) {
	e := newEnv(t, sftptest.Options{})
	e.p.host = accept
	e.p.secret = answer("secret", false)
	p := e.manager(nil).Pool(e.srv, 8)
	conns := getN(t, p, 8)
	if h, s := e.p.counts(); h != 1 || s != 1 {
		t.Fatalf("prompts: host %d, password %d; want 1 and 1", h, s)
	}
	if n := e.ss.Connections(); n != 8 {
		t.Fatalf("%d connections, want 8", n)
	}
	for _, c := range conns {
		mustStat(t, c)
	}
}

func getN(t *testing.T, p *Pool, n int) []*Conn {
	t.Helper()
	conns := make([]*Conn, n)
	errsOut := make([]error, n)
	var wg sync.WaitGroup
	for i := range conns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conns[i], errsOut[i] = p.Get(context.Background())
		}()
	}
	wg.Wait()
	for _, err := range errsOut {
		if err != nil {
			t.Fatal(err)
		}
	}
	return conns
}

func TestDeadlinePausedDuringPrompt(t *testing.T) {
	e := newEnv(t, sftptest.Options{})
	e.p.host = func(context.Context, HostKeyRequest) (bool, error) {
		time.Sleep(time.Second)
		return true, nil
	}
	e.p.secret = answer("secret", false)
	m := e.manager(func(o *Options) { o.DialTimeout = 300 * time.Millisecond })
	for _, c := range getN(t, m.Pool(e.srv, 4), 4) {
		mustStat(t, c)
	}
}

func TestDialTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	e := newEnv(t, sftptest.Options{})
	e.srv.Port = ln.Addr().(*net.TCPAddr).Port
	m := e.manager(func(o *Options) { o.DialTimeout = 200 * time.Millisecond })
	start := time.Now()
	_, err = get(t, m, e)
	mustKind(t, err, errs.ConnectionLost)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestCancelDuringPrompt(t *testing.T) {
	e := newEnv(t, sftptest.Options{})
	entered := make(chan struct{})
	e.p.host = func(ctx context.Context, r HostKeyRequest) (bool, error) {
		close(entered)
		<-ctx.Done()
		return false, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-entered
		cancel()
	}()
	_, err := e.manager(nil).Pool(e.srv, 1).Get(ctx)
	mustKind(t, err, errs.Canceled)
}
