package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"spiid/internal/errs"
)

func TestDownloadRefusesSymlinkEscape(t *testing.T) {
	e := newEnv(t, nil)
	e.remoteFile("x", 128)
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim")
	if err := os.WriteFile(victim, []byte("valuable"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, e.req("x").Local+partSuffix); err != nil {
		t.Fatal(err)
	}
	req := e.req("x")
	req.Root = e.dest
	j, _ := e.m.Add(req)
	f := e.waitState(j.ID, Failed)
	if f.ErrKind != errs.LocalWrite {
		t.Errorf("job = %+v", f)
	}
	if got, _ := os.ReadFile(victim); !bytes.Equal(got, []byte("valuable")) {
		t.Fatal("external file was written through the symlink")
	}
}

func TestDownloadRefusesSymlinkedFolder(t *testing.T) {
	e := newEnv(t, nil)
	e.remoteFile("x", 128)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(e.dest, "link")); err != nil {
		t.Fatal(err)
	}
	req := e.req("x")
	req.Root = e.dest
	req.Local = filepath.Join(e.dest, "link", "x")
	j, _ := e.m.Add(req)
	f := e.waitState(j.ID, Failed)
	if f.ErrKind != errs.LocalWrite {
		t.Errorf("job = %+v", f)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("files written through a symlinked folder: %v", entries)
	}
}

func TestDownloadPreservesForeignSidecar(t *testing.T) {
	e := newEnv(t, nil)
	e.remoteFile("x", 128)
	valuable := []byte("downloaded user data")
	side := e.req("x").Local + partSuffix
	if err := os.WriteFile(side, valuable, 0o644); err != nil {
		t.Fatal(err)
	}
	f := e.waitState(e.add("x").ID, Failed)
	const text = "was not created by Spiid; remove it to download this file"
	if f.ErrKind != errs.LocalWrite || !strings.HasSuffix(f.Err, text) {
		t.Errorf("job = %+v", f)
	}
	if got, err := os.ReadFile(side); err != nil || !bytes.Equal(got, valuable) {
		t.Fatalf("the user's part-named file was changed: %v %q", err, got)
	}
	notExist(t, e.req("x").Local)
}

func TestDownloadKeepsForeignState(t *testing.T) {
	e := newEnv(t, nil)
	e.remoteFile("x", 128)
	p := e.req("x").Local + stateSuffix
	want := []byte("valuable user state data")
	if err := os.WriteFile(p, want, 0o600); err != nil {
		t.Fatal(err)
	}
	j := e.add("x")
	f := e.waitFor(j.ID, "terminal", func(j Job) bool { return j.State == Done || j.State == Failed })
	if f.State != Failed || f.ErrKind != errs.LocalWrite {
		t.Errorf("job = %+v", f)
	}
	got, err := os.ReadFile(p)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("foreign state destroyed: read=%v contents=%q", err, got)
	}
}
