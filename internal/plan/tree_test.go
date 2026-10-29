package plan

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestExpandFile(t *testing.T) {
	e := newEnv(t, false)
	e.file(t, "/dir/a.bin")
	stamp := time.Unix(1700000000, 0)
	if err := os.Chtimes(filepath.Join(e.root, "dir/a.bin"), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	items, skipped := e.expand(t, "/dir/a.bin")
	want := []Item{{"/dir/a.bin", filepath.Join(e.dl, "a.bin"), 3, stamp}}
	if !reflect.DeepEqual(items, want) || len(skipped) != 0 {
		t.Fatalf("%+v %+v", items, skipped)
	}
}

func TestExpandFolderTree(t *testing.T) {
	both(t, func(t *testing.T, e *testEnv) {
		for _, p := range []string{"/data/photos/x.jpg", "/data/photos/2026/y.jpg", "/data/photos/2026/deep/z.jpg"} {
			e.file(t, p)
		}
		if err := os.Mkdir(filepath.Join(e.root, "data/photos/e"), 0700); err != nil {
			t.Fatal(err)
		}
		items, skipped := e.expand(t, "/data/photos")
		want := []string{"photos/2026/deep/z.jpg", "photos/2026/y.jpg", "photos/x.jpg"}
		if len(items) != len(want) || len(skipped) != 0 {
			t.Fatalf("%+v %+v", items, skipped)
		}
		for i, p := range want {
			if items[i].Local != filepath.Join(e.dl, p) || items[i].Remote != e.remote("/data/"+p) || items[i].Size != 3 ||
				items[i].ModTime.IsZero() {
				t.Fatalf("%+v", items[i])
			}
		}
	})
}

func TestExpandRootSlash(t *testing.T) {
	e := newEnv(t, false)
	e.file(t, "/a")
	e.file(t, "/dir/b")
	items, skipped := e.expand(t, "/")
	if len(items) != 2 || len(skipped) != 0 || items[0].Local != filepath.Join(e.dl, "a") ||
		items[1].Local != filepath.Join(e.dl, "dir/b") {
		t.Fatalf("%+v %+v", items, skipped)
	}
}

func TestExpandSkipsSymlinksInside(t *testing.T) {
	both(t, func(t *testing.T, e *testEnv) {
		e.file(t, "/data/a")
		e.file(t, "/target/b")
		for name, target := range map[string]string{"file": "../data/a", "folder": "../target"} {
			if err := os.Symlink(target, filepath.Join(e.root, "data", name)); err != nil {
				t.Fatal(err)
			}
		}
		items, skipped := e.expand(t, "/data")
		want := []Skipped{{e.remote("/data/file"), "symlink"}, {e.remote("/data/folder"), "symlink"}}
		if len(items) != 1 || !reflect.DeepEqual(skipped, want) {
			t.Fatalf("%+v %+v", items, skipped)
		}
	})
}

func TestExpandFollowsRootSymlink(t *testing.T) {
	e := newEnv(t, false)
	e.file(t, "/target/a")
	if err := os.Symlink("target", filepath.Join(e.root, "link")); err != nil {
		t.Fatal(err)
	}
	items, skipped := e.expand(t, "/link")
	if len(items) != 1 || len(skipped) != 0 || items[0].Remote != "/link/a" ||
		items[0].Local != filepath.Join(e.dl, "link/a") {
		t.Fatalf("%+v %+v", items, skipped)
	}
}
