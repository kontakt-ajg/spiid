package plan

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"spiid/internal/errs"
)

func TestExpandAllowsDotDotPrefixRoot(t *testing.T) {
	e := newEnv(t, false)
	e.file(t, "/..backup/f")
	e.file(t, "/..notes")
	items, skipped := e.expand(t, "/..backup", "/..notes")
	if len(items) != 2 || len(skipped) != 0 {
		t.Fatalf("items=%+v skipped=%+v", items, skipped)
	}
}

func TestExpandRejectsFileDirectoryCollision(t *testing.T) {
	e := newEnv(t, false)
	e.file(t, "/x/a")
	e.file(t, "/y/A/b")
	e.file(t, "/p/c/d")
	e.file(t, "/q/C")
	items, skipped := e.expand(t, "/x/a", "/y/A", "/p/c", "/q/C")
	if len(items) != 2 || len(skipped) != 2 {
		t.Fatalf("items=%+v skipped=%+v", items, skipped)
	}
	for _, s := range skipped {
		if !strings.HasPrefix(s.Reason, "duplicate of ") {
			t.Fatalf("reason %q", s.Reason)
		}
	}
}

func TestExpandUnsafeNames(t *testing.T) {
	e := newEnv(t, false)
	for _, p := range []string{"/data/a:b", "/data/back\\slash", "/data/ok", "/data/x:y/hidden"} {
		e.file(t, p)
	}
	items, skipped := e.expand(t, "/data")
	want := []Skipped{
		{"/data/a:b", "unsafe name"},
		{"/data/back\\slash", "unsafe name"},
		{"/data/x:y", "unsafe name"},
	}
	if len(items) != 1 || items[0].Remote != "/data/ok" || !reflect.DeepEqual(skipped, want) {
		t.Fatalf("%+v %+v", items, skipped)
	}
	items, skipped = e.expand(t, "/data/x:y")
	if len(items) != 0 || !reflect.DeepEqual(skipped, []Skipped{{"/data/x:y", "unsafe name"}}) {
		t.Fatalf("%+v %+v", items, skipped)
	}
}

func TestReservedSidecarNames(t *testing.T) {
	for _, suffix := range []string{".spiid-part", ".SPIID-PART", ".spiid-state", ".SPIID-STATE"} {
		for _, folder := range []bool{false, true} {
			for _, root := range []bool{false, true} {
				name := suffix
				if folder {
					name += "/folder"
				}
				if root {
					name += "/root"
				}
				t.Run(name, func(t *testing.T) {
					e := newEnv(t, false)
					remote := "/data/entry" + suffix
					file := remote
					if folder {
						file += "/hidden"
					}
					e.file(t, file)
					selected := "/data"
					if root {
						selected = remote
					}
					items, skipped := e.expand(t, selected)
					want := []Skipped{{remote, "reserved name"}}
					if len(items) != 0 || !reflect.DeepEqual(skipped, want) {
						t.Fatalf("items=%+v skipped=%+v, want skipped=%+v", items, skipped, want)
					}
				})
			}
		}
	}
}

func TestExpandDuplicates(t *testing.T) {
	for _, names := range [][2]string{{"a.bin", "A.BIN"}, {"S", "ſ"}, {"K", "K"}} {
		t.Run(names[0], func(t *testing.T) {
			e := newEnv(t, false)
			e.file(t, "/x/"+names[0])
			e.file(t, "/y/"+names[1])
			items, skipped := e.expand(t, "/x/"+names[0], "/y/"+names[1])
			want := []Skipped{{"/y/" + names[1], "duplicate of " + filepath.Join(e.dl, names[0])}}
			if len(items) != 1 || items[0].Remote != "/x/"+names[0] || !reflect.DeepEqual(skipped, want) {
				t.Fatalf("%+v %+v", items, skipped)
			}
		})
	}
}

func TestExpandUnlistableFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits unavailable")
	}
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if u.Uid == "0" {
		t.Skip("root bypasses directory permissions")
	}
	e := newEnv(t, false)
	e.file(t, "/data/blocked/hidden")
	e.file(t, "/data/ok")
	blocked := filepath.Join(e.root, "data/blocked")
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(blocked, 0700) })
	if _, err := os.ReadDir(blocked); !os.IsPermission(err) {
		t.Fatalf("folder is not unlistable: %v", err)
	}
	items, skipped := e.expand(t, "/data")
	if len(items) != 1 || items[0].Remote != "/data/ok" || len(skipped) != 1 || skipped[0].Remote != "/data/blocked" ||
		!strings.HasPrefix(skipped[0].Reason, "can't list: ") {
		t.Fatalf("%+v %+v", items, skipped)
	}
}

func TestExpandCancel(t *testing.T) {
	e := newEnv(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := Expand(ctx, e.c, []string{"/"}, e.dl)
	if errs.KindOf(err) != errs.Canceled {
		t.Fatal(err)
	}
}

func TestExpandBadInput(t *testing.T) {
	e := newEnv(t, false)
	for _, tc := range []struct {
		roots []string
		dl    string
	}{{[]string{"relative"}, e.dl}, {[]string{"/"}, "relative"}, {nil, e.dl + string(filepath.Separator) + "."}} {
		_, _, err := Expand(context.Background(), e.c, tc.roots, tc.dl)
		if errs.KindOf(err) != errs.InvalidInput {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
	e.file(t, "/ok")
	items, skipped, err := Expand(context.Background(), e.c, []string{"/ok", "/missing"}, e.dl)
	if errs.KindOf(err) != errs.NotFound || len(items) != 0 || len(skipped) != 0 {
		t.Fatalf("%+v %+v %v", items, skipped, err)
	}
}
