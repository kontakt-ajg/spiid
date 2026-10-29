package engine

import (
	"bytes"
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"spiid/internal/config"
)

var baseTime = time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)

type single struct{ p Pool }

func (s single) Pool(config.Server, int) Pool { return s.p }

type env struct {
	t      *testing.T
	remote string // folder served
	dest   string
	pool   *tpool
	m      *Manager
	srv    config.Server
}

func newEnv(t *testing.T, mod func(*Options)) *env {
	t.Helper()
	e := &env{t: t, remote: t.TempDir(), dest: t.TempDir(), srv: config.Server{Name: "box"}}
	e.pool = newPool(t, e.remote)
	e.m = e.newManager(mod)
	return e
}

func (e *env) newManager(mod func(*Options)) *Manager {
	o := Options{
		Pools: single{e.pool}, ThreadsPerFile: 4, ParallelFiles: 2, MaxConnections: 8,
		MinSegment: 64 << 10, RetryWaits: []time.Duration{
			10 * time.Millisecond,
			10 * time.Millisecond,
			10 * time.Millisecond,
		},
		SaveInterval: 20 * time.Millisecond, SpeedInterval: 20 * time.Millisecond,
	}
	if mod != nil {
		mod(&o)
	}
	m := NewManager(o)
	e.t.Cleanup(m.Close)
	return m
}

// remoteFile creates /name on the "server" with deterministic content and a whole-second mtime.
func (e *env) remoteFile(name string, size int) []byte {
	e.t.Helper()
	data := make([]byte, size)
	rand.New(rand.NewSource(int64(size))).Read(data)
	p := filepath.Join(e.remote, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		e.t.Fatal(err)
	}
	if err := os.Chtimes(p, baseTime, baseTime); err != nil {
		e.t.Fatal(err)
	}
	return data
}

func (e *env) req(name string) Request {
	return Request{Server: e.srv, Remote: "/" + name, Local: filepath.Join(e.dest, name)}
}

func (e *env) add(name string) Job {
	j, _ := e.m.Add(e.req(name))
	return j
}

func (e *env) job(id int64) Job {
	for _, j := range e.m.Jobs() {
		if j.ID == id {
			return j
		}
	}
	e.t.Fatalf("job %d not found", id)
	return Job{}
}

// waitFor polls until cond(job) holds.
func (e *env) waitFor(id int64, what string, cond func(Job) bool) Job {
	e.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if j := e.job(id); cond(j) {
			return j
		}
		time.Sleep(time.Millisecond)
	}
	e.t.Fatalf("timeout waiting for %s; job = %+v", what, e.job(id))
	return Job{}
}

func (e *env) waitState(id int64, s State) Job {
	e.t.Helper()
	return e.waitFor(id, s.String(), func(j Job) bool { return j.State == s })
}

// waitPartial waits until 0 < Done < Size, and asserts that the job really is partial.
func (e *env) waitPartial(id int64) Job {
	e.t.Helper()
	j := e.waitFor(id, "partial progress", func(j Job) bool { return j.Done > 0 || j.State > Running })
	if j.State != Running || j.Done <= 0 || j.Done >= j.Size {
		e.t.Fatalf("job is not partial: %+v", j)
	}
	return j
}

func equalFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s: content differs (%d bytes, want %d)", path, len(got), len(want))
	}
}

func notExist(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s should not exist (err %v)", p, err)
		}
	}
}

func exists(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s should exist: %v", p, err)
		}
	}
}

// savedDone returns the sum of "done" in the state file of local.
func savedDone(t *testing.T, local string) int64 {
	t.Helper()
	data, err := os.ReadFile(local + stateSuffix)
	if err != nil {
		t.Fatal(err)
	}
	st, err := decodeState(data)
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	for _, s := range st.Segments {
		n += s.Done
	}
	return n
}

// waitManager returns nil when nothing is queued or running, or ctx's error.
func waitManager(ctx context.Context, m *Manager) error {
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		busy := false
		for _, j := range m.Jobs() {
			if j.State == Queued || j.State == Running {
				busy = true
			}
		}
		if !busy {
			return nil
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
