package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spiid/internal/errs"
)

func TestDownloadSmall(t *testing.T) {
	e := newEnv(t, nil)
	want := e.remoteFile("a.bin", 10000)
	j := e.add("a.bin")
	j = e.waitState(j.ID, Done)
	local := e.req("a.bin").Local
	equalFile(t, local, want)
	fi, _ := os.Stat(local)
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", fi.Mode().Perm())
	}
	if !fi.ModTime().Equal(baseTime) {
		t.Errorf("mtime = %v, want %v", fi.ModTime(), baseTime)
	}
	notExist(t, local+partSuffix, local+stateSuffix)
	if j.Segments != 1 || j.Size != 10000 || j.Done != 10000 || j.Speed != 0 {
		t.Errorf("job = %+v", j)
	}
	if e.pool.verify() == 0 {
		t.Error("no connection was used")
	}
}

func TestDownloadEmptyFile(t *testing.T) {
	e := newEnv(t, nil)
	e.remoteFile("e.bin", 0)
	j := e.waitState(e.add("e.bin").ID, Done)
	local := e.req("e.bin").Local
	fi, err := os.Stat(local)
	if err != nil || fi.Size() != 0 || fi.Mode().Perm() != 0o644 || !fi.ModTime().Equal(baseTime) {
		t.Fatalf("local = %v %v", fi, err)
	}
	notExist(t, local+partSuffix, local+stateSuffix)
	if j.Segments != 0 {
		t.Errorf("Segments = %d, want 0", j.Segments)
	}
}

func TestDownloadSegments(t *testing.T) {
	e := newEnv(t, nil)
	want := e.remoteFile("big.bin", 2<<20)
	j := e.waitState(e.add("big.bin").ID, Done)
	if j.Segments != 4 {
		t.Errorf("Segments = %d, want 4", j.Segments)
	}
	equalFile(t, e.req("big.bin").Local, want)
}

func TestDownloadCreatesFolders(t *testing.T) {
	e := newEnv(t, nil)
	want := e.remoteFile("c.bin", 100)
	req := e.req("c.bin")
	req.Local = filepath.Join(e.dest, "x", "y", "c.bin")
	j, _ := e.m.Add(req)
	e.waitState(j.ID, Done)
	equalFile(t, req.Local, want)
}

func TestNotARegularFile(t *testing.T) {
	e := newEnv(t, nil)
	os.Mkdir(filepath.Join(e.remote, "dir"), 0o755)
	j := e.waitState(e.add("dir").ID, Failed)
	if j.ErrKind != errs.InvalidInput || !strings.Contains(j.Err, "not a regular file") {
		t.Errorf("job = %+v", j)
	}
}

func TestLocalExistsSame(t *testing.T) {
	e := newEnv(t, nil)
	want := e.remoteFile("s.bin", 5000)
	local := e.req("s.bin").Local
	os.WriteFile(local, want, 0o644)
	os.Chtimes(local, baseTime, baseTime)
	os.WriteFile(local+partSuffix, []byte("x"), 0o600)
	st, _ := json.Marshal(stateFile{RemotePath: "/s.bin", Size: 5000, MTime: baseTime.Unix(),
		Segments: []Segment{{0, 5000, 100}}})
	os.WriteFile(local+stateSuffix, st, 0o600)
	j := e.waitState(e.add("s.bin").ID, Skipped)
	notExist(t, local+partSuffix, local+stateSuffix)
	equalFile(t, local, want)
	if j.Err != "" {
		t.Errorf("Err = %q", j.Err)
	}
}

func TestLocalExistsDiffers(t *testing.T) {
	e := newEnv(t, nil)
	want := e.remoteFile("d.bin", 5000)
	local := e.req("d.bin").Local
	os.WriteFile(local, []byte("old content"), 0o644)
	j := e.waitState(e.add("d.bin").ID, Failed)
	if j.ErrKind != errs.LocalExists || j.Err != "local file "+local+" exists and differs" {
		t.Fatalf("job = %+v", j)
	}
	equalFile(t, local, []byte("old content"))
	if err := e.m.Retry(j.ID); err != nil { // retry alone must fail the same way
		t.Fatal(err)
	}
	e.waitFor(j.ID, "failed again", func(j Job) bool { return j.State == Failed })
	if err := e.m.Overwrite(j.ID); err != nil {
		t.Fatal(err)
	}
	e.waitState(j.ID, Done)
	equalFile(t, local, want)
	if err := e.m.Overwrite(j.ID); errs.KindOf(err) != errs.InvalidInput {
		t.Errorf("Overwrite on a done job: %v", err)
	}
}

func TestStateFileFormat(t *testing.T) {
	e := newEnv(t, nil)
	e.pool.setDelay(3 * time.Millisecond)
	e.remoteFile("f.bin", 4<<20)
	local := e.req("f.bin").Local
	j := e.add("f.bin")
	e.waitPartial(j.ID)
	var raw map[string]json.RawMessage
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(local + stateSuffix)
		if err == nil && json.Unmarshal(data, &raw) == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no valid state file: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	for _, k := range []string{"remote_path", "size", "mtime", "segments"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("state file lacks %q", k)
		}
	}
	if string(raw["mtime"]) != "1577934245" || string(raw["remote_path"]) != `"/f.bin"` {
		t.Errorf("state = %s", raw)
	}
	fi, err := os.Stat(local + stateSuffix)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("state mode: %v %v", fi, err)
	}
	pi, err := os.Stat(local + partSuffix)
	if err != nil || pi.Mode().Perm() != 0o600 {
		t.Errorf("part mode: %v %v", pi, err)
	}
	if err := e.m.Cancel(j.ID); err != nil {
		t.Fatal(err)
	}
	e.waitState(j.ID, Canceled)
	entries, _ := os.ReadDir(e.dest)
	for _, en := range entries {
		if strings.Contains(en.Name(), ".tmp-") {
			t.Errorf("leftover temp file %s", en.Name())
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := waitManager(ctx, e.m); err != nil {
		t.Fatal(err)
	}
}

func TestEmptyOverwriteSetsCompletedPermissions(t *testing.T) {
	e := newEnv(t, nil)
	e.remoteFile("empty", 0)
	req := e.req("empty")
	req.Overwrite = true
	os.WriteFile(req.Local, []byte("old"), 0o600)
	j, _ := e.m.Add(req)
	e.waitState(j.ID, Done)
	st, _ := os.Stat(req.Local)
	if st.Mode().Perm() != 0o644 {
		t.Fatalf("completed mode = %o, want 644", st.Mode().Perm())
	}
}
