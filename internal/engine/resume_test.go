package engine

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// slowEnv has a file big enough to cancel in the middle.
func slowEnv(t *testing.T, mod func(*Options)) (*env, []byte) {
	e := newEnv(t, func(o *Options) {
		o.ThreadsPerFile = 2
		if mod != nil {
			mod(o)
		}
	})
	e.pool.setDelay(4 * time.Millisecond)
	return e, e.remoteFile("r.bin", 4<<20)
}

// watchNotBelow polls until the job is Done and fails if Done ever drops below min.
func watchNotBelow(e *env, id int64, min int64) {
	e.t.Helper()
	e.waitFor(id, "done", func(j Job) bool {
		if j.Segments > 0 && j.Done < min { // Segments > 0: the state has been read
			e.t.Fatalf("job restarted instead of resuming: Done = %d, saved %d (%+v)", j.Done, min, j)
		}
		return j.State == Done
	})
}

func TestCancelThenRetryResumes(t *testing.T) {
	e, want := slowEnv(t, nil)
	local := e.req("r.bin").Local
	j := e.add("r.bin")
	p := e.waitPartial(j.ID)
	if err := e.m.Cancel(j.ID); err != nil {
		t.Fatal(err)
	}
	c := e.waitState(j.ID, Canceled)
	if c.Done < p.Done || c.Done >= c.Size {
		t.Fatalf("canceled job is not partial: %+v", c)
	}
	exists(t, local+partSuffix, local+stateSuffix)
	saved := savedDone(t, local)
	if saved <= 0 || saved != c.Done {
		t.Fatalf("saved bytes = %d, job Done = %d", saved, c.Done)
	}
	if err := e.m.Retry(j.ID); err != nil {
		t.Fatal(err)
	}
	watchNotBelow(e, j.ID, saved)
	equalFile(t, local, want)
	notExist(t, local+partSuffix, local+stateSuffix)
	e.pool.verify()
}

func TestResumeInNewManager(t *testing.T) {
	e, want := slowEnv(t, nil)
	local := e.req("r.bin").Local
	j := e.add("r.bin")
	e.waitPartial(j.ID)
	e.m.Close()
	closed := e.job(j.ID)
	if closed.State != Canceled || closed.Done >= closed.Size {
		t.Fatalf("after Close: %+v", closed)
	}
	exists(t, local+partSuffix, local+stateSuffix)
	saved := savedDone(t, local)
	if saved <= 0 {
		t.Fatal("nothing saved")
	}
	m2 := e.newManager(func(o *Options) { o.ThreadsPerFile = 2 })
	e.m = m2
	j2 := e.add("r.bin")
	watchNotBelow(e, j2.ID, saved)
	equalFile(t, local, want)
}

func TestStateMismatchStartsFresh(t *testing.T) {
	e := newEnv(t, nil)
	want := e.remoteFile("m.bin", 1<<20)
	local := e.req("m.bin").Local
	garbage := bytes.Repeat([]byte{0xFF}, 1<<20)
	os.WriteFile(local+partSuffix, garbage, 0o600)
	st := stateFile{
		RemotePath: "/m.bin",
		Size:       1 << 20,
		MTime:      baseTime.Unix() + 1,
		Segments:   []Segment{{0, 1 << 20, 1 << 20}},
	}
	data, _ := json.Marshal(st)
	os.WriteFile(local+stateSuffix, data, 0o600)
	e.waitState(e.add("m.bin").ID, Done)
	equalFile(t, local, want)
}

func TestSpeedNoSpikeAfterResume(t *testing.T) {
	const size = 2 << 20
	e := newEnv(t, func(o *Options) { o.ThreadsPerFile = 1 })
	want := e.remoteFile("v.bin", size)
	local := e.req("v.bin").Local
	done := int64(size * 9 / 10)
	part := make([]byte, size)
	copy(part, want[:done])
	os.WriteFile(local+partSuffix, part, 0o600)
	st := stateFile{
		RemotePath: "/v.bin",
		Size:       size,
		MTime:      baseTime.Unix(),
		Segments:   []Segment{{0, size, done}},
	}
	data, _ := json.Marshal(st)
	os.WriteFile(local+stateSuffix, data, 0o600)
	e.pool.setDelay(30 * time.Millisecond)

	start := time.Now()
	j := e.add("v.bin")
	var maxSpeed float64
	var samples int
	e.waitFor(j.ID, "done", func(j Job) bool {
		if j.Speed > maxSpeed {
			maxSpeed = j.Speed
		}
		if j.Speed > 0 {
			samples++
		}
		return j.State == Done
	})
	total := time.Since(start).Seconds()
	limit := 2 * float64(size) / total
	if maxSpeed >= limit {
		t.Errorf("speed spike: max %.0f B/s, limit %.0f B/s", maxSpeed, limit)
	}
	if samples == 0 {
		t.Error("never saw a speed value")
	}
	equalFile(t, local, want)
}

func TestResumeAfterShortReads(t *testing.T) {
	e, want := slowEnv(t, func(o *Options) { o.ThreadsPerFile = 1 })
	e.pool.maxRead.Store(1024)
	j := e.add("r.bin")
	e.waitFor(j.ID, "progress", func(j Job) bool { return j.Done >= 4096 })
	e.m.Cancel(j.ID)
	c := e.waitState(j.ID, Canceled)
	if c.Done <= 0 || c.Done >= c.Size {
		t.Fatalf("not partial: %+v", c)
	}
	t.Logf("partial bytes saved: %d/%d", c.Done, c.Size)
	e.pool.setDelay(0)
	e.m.Retry(j.ID)
	e.waitState(j.ID, Done)
	equalFile(t, e.req("r.bin").Local, want)
}

func TestResumeRequiresSameServer(t *testing.T) {
	e, want := slowEnv(t, nil)
	j := e.add("r.bin")
	e.waitPartial(j.ID)
	e.m.Close()
	local := e.req("r.bin").Local
	if savedDone(t, local) <= 0 {
		t.Fatal("not partial")
	}
	other := bytes.Repeat([]byte{'B'}, len(want))
	remote := filepath.Join(e.remote, "r.bin")
	if err := os.WriteFile(remote, other, 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(remote, baseTime, baseTime)
	e.pool.setDelay(0)
	// A second server has the same path, size and mtime, but different contents.
	e.srv.Name, e.srv.Host = "other-server", "other-host"
	e.m = e.newManager(nil)
	e.waitState(e.add("r.bin").ID, Done)
	equalFile(t, local, other)
}
