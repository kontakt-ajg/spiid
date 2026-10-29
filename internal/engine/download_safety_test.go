package engine

import (
	"fmt"
	"os"
	"testing"
	"time"

	"spiid/internal/errs"
)

func TestLinkFallback(t *testing.T) {
	old := linkFile
	linkFile = func(a, b string) error { return &os.LinkError{Op: "link", Old: a, New: b, Err: os.ErrPermission} }
	defer func() { linkFile = old }()
	for _, existing := range []bool{false, true} {
		dir := t.TempDir()
		part, local := dir+"/part", dir+"/local"
		f, err := openPart(part, 3, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteAt([]byte("new"), 0); err != nil {
			t.Fatal(err)
		}
		if existing {
			os.WriteFile(local, []byte("old"), 0600)
		}
		err = finishPart(f, part, local, time.Now(), false)
		if existing {
			if errs.KindOf(err) != errs.LocalExists {
				t.Fatal(err)
			}
			equalFile(t, local, []byte("old"))
			exists(t, part)
		} else {
			if err != nil {
				t.Fatal(err)
			}
			equalFile(t, local, []byte("new"))
			notExist(t, part)
			info, _ := os.Stat(local)
			if info.Mode().Perm() != finishedMode {
				t.Fatalf("mode %o", info.Mode().Perm())
			}
		}
	}
}

func TestEmptyExclusive(t *testing.T) {
	path := t.TempDir() + "/empty"
	os.WriteFile(path, []byte("valuable"), 0600)
	j := &job{req: Request{Local: path}, mtime: time.Now()}
	if err := j.finishEmpty(); errs.KindOf(err) != errs.LocalExists {
		t.Fatal(err)
	}
	equalFile(t, path, []byte("valuable"))
	j.req.Overwrite = true
	if err := j.finishEmpty(); err != nil {
		t.Fatal(err)
	}
	equalFile(t, path, nil)
	info, _ := os.Stat(path)
	if info.Mode().Perm() != finishedMode {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
}

func TestReaddOverwriteAndClear(t *testing.T) {
	e := newEnv(t, nil)
	e.remoteFile("x", 128)
	req := e.req("x")
	os.WriteFile(req.Local, []byte("old"), 0600)
	first, _ := e.m.Add(req)
	e.waitState(first.ID, Failed)
	e.m.ClearFinished()
	if len(e.m.Jobs()) != 1 {
		t.Fatal("cleared failed job")
	}
	req.Overwrite = true
	second, duplicate := e.m.Add(req)
	if duplicate || first.ID != second.ID {
		t.Fatal("did not restart failed job")
	}
	e.waitState(second.ID, Done)
	os.WriteFile(req.Local, []byte("old again"), 0600)
	third, duplicate := e.m.Add(req)
	if duplicate || first.ID != third.ID {
		t.Fatal("did not restart done job")
	}
	e.waitState(third.ID, Done)
	e.m.ClearFinished()
	if len(e.m.Jobs()) != 0 {
		t.Fatal("finished job retained")
	}
	fourth, duplicate := e.m.Add(req)
	if duplicate || fourth.ID == third.ID {
		t.Fatal("dedup entry retained")
	}
}

func TestClearFinishedStates(t *testing.T) {
	m := NewManager(Options{})
	m.closed = true // Keep the synthetic queued and running snapshots from starting workers.
	for i, state := range []State{Done, Skipped, Canceled, Failed, Queued, Running} {
		req := Request{Local: fmt.Sprint(i)}
		m.Add(req)
		m.jobs[i].pub.State = state
	}
	m.ClearFinished()
	jobs := m.Jobs()
	if len(jobs) != 3 || len(m.byKey) != 3 {
		t.Fatalf("jobs %v, keys %v", jobs, m.byKey)
	}
	for _, j := range jobs {
		if j.State != Failed && j.State != Queued && j.State != Running {
			t.Fatalf("retained %+v", j)
		}
	}
}
