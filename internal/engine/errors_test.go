package engine

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"spiid/internal/errs"
)

func TestDropMidwayRetries(t *testing.T) {
	e, want := slowEnv(t, nil)
	j := e.add("r.bin")
	e.waitPartial(j.ID)
	e.pool.dropAll()
	e.waitState(j.ID, Done)
	equalFile(t, e.req("r.bin").Local, want)
	e.pool.verify()
}

func appendRemote(t *testing.T, path string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("more")
	f.Close()
}

func TestRemoteChangedMidway(t *testing.T) {
	e, _ := slowEnv(t, nil)
	local := e.req("r.bin").Local
	j := e.add("r.bin")
	e.waitPartial(j.ID)
	appendRemote(t, filepath.Join(e.remote, "r.bin"))
	f := e.waitState(j.ID, Failed)
	if f.ErrKind != errs.RemoteChanged || f.Err != "remote file changed; add it again" {
		t.Errorf("job = %+v", f)
	}
	notExist(t, local+partSuffix, local+stateSuffix, local)
}

func TestRemoteChangedAtEnd(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.ThreadsPerFile = 1 })
	e.remoteFile("x.bin", 300000)
	remote := filepath.Join(e.remote, "x.bin")
	later := baseTime.Add(time.Hour)
	e.pool.beforeGet = func(n int32) {
		if n != 3 {
			return
		}
		os.Chtimes(remote, later, later)
	}
	local := e.req("x.bin").Local
	f := e.waitState(e.add("x.bin").ID, Failed)
	if f.ErrKind != errs.RemoteChanged || f.Err != "remote file changed; add it again" {
		t.Errorf("job = %+v", f)
	}
	notExist(t, local+partSuffix, local+stateSuffix, local)
	if f.Done != f.Size {
		t.Errorf("all bytes should have been read before the final check: %+v", f)
	}
}

func TestPermanentNoRetry(t *testing.T) {
	e := newEnv(t, func(o *Options) {
		o.RetryWaits = []time.Duration{5 * time.Second, 5 * time.Second, 5 * time.Second}
	})
	start := time.Now()
	f := e.waitState(e.add("missing.bin").ID, Failed)
	if f.ErrKind != errs.NotFound {
		t.Errorf("job = %+v", f)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %v, a permanent error must not wait", d)
	}
}

func TestTransientGivesUp(t *testing.T) {
	e := newEnv(t, nil)
	e.pool.setFail(errs.New(errs.ConnectionLost, "dial", "box", io.EOF))
	start := time.Now()
	f := e.waitState(e.add("any.bin").ID, Failed)
	if f.ErrKind != errs.ConnectionLost {
		t.Errorf("job = %+v", f)
	}
	if d := time.Since(start); d < 30*time.Millisecond {
		t.Errorf("gave up after %v, want >= 30ms (3 waits)", d)
	}
	if n := e.pool.gets.Load(); n != 4 {
		t.Errorf("attempts = %d, want 4", n)
	}
}

func TestCancelInterruptsBlockedRead(t *testing.T) {
	e := newEnv(t, nil)
	e.pool.setDelay(10 * time.Second)
	e.remoteFile("b.bin", 1<<20)
	j := e.add("b.bin")
	e.waitFor(j.ID, "a worker with a connection", func(j Job) bool { return j.ActiveSegments > 0 })
	time.Sleep(50 * time.Millisecond) // let the READ block on the server's delay
	start := time.Now()
	if err := e.m.Cancel(j.ID); err != nil {
		t.Fatal(err)
	}
	e.waitState(j.ID, Canceled)
	if d := time.Since(start); d > time.Second {
		t.Errorf("cancel took %v", d)
	}
	e.pool.verify()
}

func TestConnectionReturnedOnce(t *testing.T) {
	e, _ := slowEnv(t, nil)
	e.remoteFile("one.bin", 200000)
	e.waitState(e.add("one.bin").ID, Done)

	b := e.add("r.bin") // canceled midway
	e.waitPartial(b.ID)
	e.m.Cancel(b.ID)
	e.waitState(b.ID, Canceled)

	e.m.Retry(b.ID) // dropped midway
	e.waitFor(b.ID, "running again", func(j Job) bool { return j.ActiveSegments > 0 })
	e.pool.dropAll()
	e.waitState(b.ID, Done)

	e.waitState(e.add("nothere.bin").ID, Failed)
	e.m.Close()
	if e.pool.verify() == 0 {
		t.Fatal("no connections used")
	}
}
