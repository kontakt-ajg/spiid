package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"spiid/internal/errs"
)

func TestAddSameLocal(t *testing.T) {
	e := newEnv(t, nil)
	e.remoteFile("a.bin", 1000)
	e.pool.setDelay(20 * time.Millisecond)
	req := e.req("a.bin")
	j1, existed1 := e.m.Add(req)
	j2, existed2 := e.m.Add(req)
	if existed1 || !existed2 || j1.ID != j2.ID || j1.ID != 1 {
		t.Fatalf("first %v %v, second %v %v", j1.ID, existed1, j2.ID, existed2)
	}
	up := req
	up.Local = strings.ToUpper(req.Local)
	j3, existed3 := e.m.Add(up)
	if !existed3 || j3.ID != j1.ID {
		t.Errorf("A.BIN vs a.bin: id %d existed %v", j3.ID, existed3)
	}
	e.waitState(j1.ID, Done)
	if _, existed := e.m.Add(req); existed {
		t.Error("a finished job must restart")
	}
	other := req
	other.Local += "2"
	if j, existed := e.m.Add(other); existed || j.ID != 2 {
		t.Errorf("different local: %+v %v", j, existed)
	}
	jobs := e.m.Jobs()
	if len(jobs) != 2 || jobs[0].ID != 2 || jobs[1].ID != 1 {
		t.Errorf("Jobs not newest first: %+v", jobs)
	}
}

func TestCancelAndRetryErrors(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.ParallelFiles = 1 })
	var retried []string
	e.m.opts.OnRetry = func(s string) { retried = append(retried, s) }
	e.pool.setDelay(20 * time.Millisecond)
	e.remoteFile("a.bin", 300000)
	e.remoteFile("b.bin", 300000)
	a, b := e.add("a.bin"), e.add("b.bin")
	if got := e.job(b.ID).State; got != Queued {
		t.Fatalf("second job state = %v, want queued", got)
	}
	if err := e.m.Cancel(b.ID); err != nil || e.job(b.ID).State != Canceled {
		t.Fatalf("cancel queued: %v %v", err, e.job(b.ID).State)
	}
	if err := e.m.Cancel(999); errs.KindOf(err) != errs.InvalidInput || !strings.Contains(err.Error(), "no such job") {
		t.Errorf("unknown id: %v", err)
	}
	if err := e.m.Retry(a.ID); errs.KindOf(err) != errs.InvalidInput {
		t.Errorf("retry of a running job: %v", err)
	}
	e.m.Cancel(a.ID)
	e.waitState(a.ID, Canceled)
	if err := e.m.Cancel(a.ID); errs.KindOf(err) != errs.InvalidInput ||
		!strings.Contains(err.Error(), "job is not active") {
		t.Errorf("cancel of a canceled job: %v", err)
	}
	e.pool.setDelay(0)
	if err := e.m.Retry(a.ID); err != nil {
		t.Fatal(err)
	}
	if len(retried) != 1 || retried[0] != "box" {
		t.Errorf("OnRetry calls = %v", retried)
	}
	e.waitState(a.ID, Done)
}

func TestParallelLimit(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.ParallelFiles = 2 })
	e.pool.setDelay(2 * time.Millisecond)
	var ids []int64
	for _, n := range []string{"1", "2", "3", "4", "5"} {
		e.remoteFile(n+".bin", 300000)
		ids = append(ids, e.add(n+".bin").ID)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	stop := make(chan struct{})
	var peak int
	go func() {
		defer wg.Done()
		for {
			n := 0
			for _, j := range e.m.Jobs() {
				if j.State == Running {
					n++
				}
			}
			peak = max(peak, n)
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := waitManager(ctx, e.m); err != nil {
		t.Fatal(err)
	}
	close(stop)
	wg.Wait()
	if peak > 2 || peak == 0 {
		t.Errorf("peak running = %d, want 1..2", peak)
	}
	for _, id := range ids {
		if s := e.job(id).State; s != Done {
			t.Errorf("job %d: %v", id, s)
		}
	}
}

func TestSetLimits(t *testing.T) {
	e := newEnv(t, nil)
	e.remoteFile("a.bin", 2<<20)
	e.remoteFile("b.bin", 2<<20)
	e.m.SetLimits(2, 2, 8)
	a := e.waitState(e.add("a.bin").ID, Done)
	e.m.SetLimits(4, 2, 8)
	b := e.waitState(e.add("b.bin").ID, Done)
	if a.Segments != 2 || b.Segments != 4 {
		t.Errorf("segments %d and %d, want 2 and 4", a.Segments, b.Segments)
	}
}

func TestSetLimitsParallelStartsQueued(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.ParallelFiles = 1 })
	e.pool.setDelay(20 * time.Millisecond)
	var ids []int64
	for _, n := range []string{"1", "2", "3"} {
		e.remoteFile(n+".bin", 300000)
		ids = append(ids, e.add(n+".bin").ID)
	}
	e.m.SetLimits(4, 3, 8)
	for _, id := range ids {
		if s := e.job(id).State; s != Running {
			t.Errorf("job %d is %v, want running", id, s)
		}
	}
	e.m.Close()
}

func TestCanceledJobHasCanceledError(t *testing.T) {
	e, _ := slowEnv(t, nil)
	j := e.add("r.bin")
	e.waitPartial(j.ID)
	e.m.Cancel(j.ID)
	c := e.waitState(j.ID, Canceled)
	if c.ErrKind != errs.Canceled || c.Err == "" {
		t.Fatalf("canceled ErrKind=%v Err=%q", c.ErrKind, c.Err)
	}
}
