package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteFileAtomicReplaces(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(p, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil || string(got) != "new" {
		t.Fatalf("content %q, err %v", got, err)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(p)
		if st.Mode().Perm() != 0o600 {
			t.Errorf("mode %v", st.Mode().Perm())
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file left: %s", e.Name())
		}
	}
}

func TestWriteFileAtomicMissingDir(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "missing", "f.txt")
	if err := WriteFileAtomic(p, []byte("x"), 0o600); err == nil {
		t.Fatal("expected error")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("created %d entries", len(entries))
	}
}

func TestExpandHome(t *testing.T) {
	home := filepath.Join("h", "me")
	tests := map[string]string{
		"~":       home,
		"~/x":     filepath.Join(home, "x"),
		"~/x/y":   filepath.Join(home, "x", "y"),
		"/abs":    "/abs",
		"rel/~":   "rel/~",
		"~user/x": "~user/x",
		"":        "",
		"~x":      "~x",
	}
	for in, want := range tests {
		if got := ExpandHome(in, home); got != want {
			t.Errorf("ExpandHome(%q) = %q, want %q", in, got, want)
		}
	}
	if runtime.GOOS == "windows" {
		if got := ExpandHome(`~\x`, home); got != filepath.Join(home, "x") {
			t.Errorf("windows: %q", got)
		}
	} else if got := ExpandHome(`~\x`, home); got != `~\x` {
		t.Errorf("non-windows: %q", got)
	}
}
