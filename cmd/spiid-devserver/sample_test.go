package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// A destination that appears while the sample is being generated must not be replaced.
func TestSamplePreservesConcurrentDestination(t *testing.T) {
	dir := t.TempDir()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	p := filepath.Join(dir, "sample-0.bin")
	err = writeIfMissing(r, "sample-0.bin", func(w io.Writer) error {
		err := os.WriteFile(p, []byte("existing data"), 0o600)
		if err != nil {
			return err
		}
		_, err = io.WriteString(w, "sample data")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "existing data" {
		t.Errorf("existing file overwritten: %q", b)
	}
	list, _ := os.ReadDir(dir)
	if len(list) != 1 {
		t.Errorf("temporary file left behind: %v", list)
	}
}

// A symlinked tree (or a deeper level) must be refused, and nothing may be written through it.
func TestSampleRefusesSymlinkedTree(t *testing.T) {
	for _, sub := range []string{"tree", filepath.Join("tree", "level2")} {
		root, outside := t.TempDir(), t.TempDir()
		err := os.MkdirAll(filepath.Join(root, "tree"), 0o755)
		if err != nil {
			t.Fatal(err)
		}
		err = os.Remove(filepath.Join(root, "tree"))
		if err != nil {
			t.Fatal(err)
		}
		if sub == "tree" {
			err = os.Symlink(outside, filepath.Join(root, "tree"))
		} else {
			err = os.MkdirAll(filepath.Join(root, "tree"), 0o755)
			if err == nil {
				err = os.Symlink(outside, filepath.Join(root, sub))
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		err = os.WriteFile(filepath.Join(root, "sample-0.bin"), nil, 0o600) // existing: skipped
		if err != nil {
			t.Fatal(err)
		}
		err = makeSample(root, 1)
		if err == nil {
			t.Errorf("%s: symlink was accepted", sub)
		}
		list, _ := os.ReadDir(outside)
		if len(list) != 0 {
			t.Errorf("%s: wrote outside root through symlink: %v", sub, list)
		}
	}
}
