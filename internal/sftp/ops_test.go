package sftp_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"spiid/internal/errs"
	"spiid/internal/sftptest"
)

var bg = context.Background()

func TestRealPath(t *testing.T) {
	forEach(t, sftptest.Options{}, nil, func(t *testing.T, e *env) {
		err := os.Mkdir(filepath.Join(e.root, "sub"), 0o755)
		if err != nil {
			t.Fatal(err)
		}
		got, err := e.c.RealPath(bg, e.rp("/sub/../sub/."))
		if err != nil {
			t.Fatal(err)
		}
		want := e.rp("/sub")
		if got != want {
			t.Fatalf("RealPath = %q, want %q", got, want)
		}
		if e.kind != "openssh" {
			for _, p := range []string{"", "."} {
				got, err = e.c.RealPath(bg, p)
				if err != nil || got != "/" {
					t.Fatalf("RealPath(%q) = %q, %v; want /", p, got, err)
				}
			}
		}
	})
}

func TestStatLstat(t *testing.T) {
	forEach(t, sftptest.Options{}, nil, func(t *testing.T, e *env) {
		data := writeRandomFile(t, filepath.Join(e.root, "file"), 1234)
		err := os.Mkdir(filepath.Join(e.root, "dir"), 0o755)
		if err != nil {
			t.Fatal(err)
		}
		err = os.Symlink(filepath.Join(e.root, "file"), filepath.Join(e.root, "link"))
		if err != nil {
			t.Fatal(err)
		}
		fi, err := e.c.Stat(bg, e.rp("/file"))
		if err != nil || !fi.IsRegular() || fi.Size != int64(len(data)) || fi.Name != "file" || fi.ModTime.IsZero() {
			t.Fatalf("file: %+v, %v", fi, err)
		}
		if fi.Mode.Perm() != 0o644 {
			t.Fatalf("file perm %v", fi.Mode.Perm())
		}
		fi, err = e.c.Stat(bg, e.rp("/dir"))
		if err != nil || !fi.IsDir() {
			t.Fatalf("dir: %+v, %v", fi, err)
		}
		fi, err = e.c.Stat(bg, e.rp("/link"))
		if err != nil || !fi.IsRegular() || fi.IsSymlink() || fi.Size != int64(len(data)) {
			t.Fatalf("Stat(link) must follow: %+v, %v", fi, err)
		}
		fi, err = e.c.Lstat(bg, e.rp("/link"))
		if err != nil || !fi.IsSymlink() || fi.IsRegular() {
			t.Fatalf("Lstat(link) must not follow: %+v, %v", fi, err)
		}
	})
}

func TestReadDir(t *testing.T) {
	forEach(t, sftptest.Options{}, nil, func(t *testing.T, e *env) {
		want := map[string]int64{}
		for i := 0; i < 120; i++ {
			name := fmt.Sprintf("f%03d", i)
			content := make([]byte, i)
			err := os.WriteFile(filepath.Join(e.root, name), content, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			want[name] = int64(i)
		}
		err := os.Mkdir(filepath.Join(e.root, "folder"), 0o755)
		if err != nil {
			t.Fatal(err)
		}
		list, err := e.c.ReadDir(bg, e.rp("/"))
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 121 {
			t.Fatalf("%d entries, want 121", len(list))
		}
		seen := map[string]bool{}
		for _, fi := range list {
			if fi.Name == "." || fi.Name == ".." {
				t.Fatalf("%q must be dropped", fi.Name)
			}
			if seen[fi.Name] {
				t.Fatalf("duplicate %q", fi.Name)
			}
			seen[fi.Name] = true
			if fi.Name == "folder" {
				if !fi.IsDir() {
					t.Fatal("folder is not a dir")
				}
				continue
			}
			size, ok := want[fi.Name]
			if !ok || fi.IsDir() || fi.Size != size {
				t.Fatalf("bad entry %+v", fi)
			}
		}
	})
}

func TestNotFound(t *testing.T) {
	forEach(t, sftptest.Options{}, nil, func(t *testing.T, e *env) {
		missing := e.rp("/missing")
		_, err := e.c.Stat(bg, missing)
		if errs.KindOf(err) != errs.NotFound {
			t.Errorf("Stat: %v", err)
		}
		_, err = e.c.Open(bg, missing)
		if errs.KindOf(err) != errs.NotFound {
			t.Errorf("Open: %v", err)
		}
		_, err = e.c.ReadDir(bg, missing)
		if errs.KindOf(err) != errs.NotFound {
			t.Errorf("ReadDir: %v", err)
		}
		_, err = e.c.Lstat(bg, missing)
		if errs.KindOf(err) != errs.NotFound {
			t.Errorf("Lstat: %v", err)
		}
	})
}

func TestPermissionDenied(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file modes are not enforced here")
	}
	forEach(t, sftptest.Options{}, nil, func(t *testing.T, e *env) {
		p := filepath.Join(e.root, "secret")
		err := os.WriteFile(p, []byte("x"), 0o644)
		if err != nil {
			t.Fatal(err)
		}
		err = os.Chmod(p, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(p, 0o644)
		_, err = e.c.Open(bg, e.rp("/secret"))
		if errs.KindOf(err) != errs.PermissionDenied {
			t.Fatalf("Open: %v", err)
		}
	})
}

func TestConcurrentStats(t *testing.T) {
	forEach(t, sftptest.Options{}, nil, func(t *testing.T, e *env) {
		writeRandomFile(t, filepath.Join(e.root, "f"), 10)
		var wg sync.WaitGroup
		errc := make(chan error, 1000)
		for g := 0; g < 50; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 20; i++ {
					fi, err := e.c.Stat(bg, e.rp("/f"))
					if err != nil || fi.Size != 10 {
						errc <- fmt.Errorf("stat: %+v %v", fi, err)
						return
					}
				}
			}()
		}
		wg.Wait()
		close(errc)
		for err := range errc {
			t.Error(err)
		}
	})
}

func TestCloseIdempotent(t *testing.T) {
	forEach(t, sftptest.Options{}, nil, func(t *testing.T, e *env) {
		writeRandomFile(t, filepath.Join(e.root, "f"), 10)
		f, err := e.c.Open(bg, e.rp("/f"))
		if err != nil {
			t.Fatal(err)
		}
		if err = f.Close(); err != nil {
			t.Fatal(err)
		}
		if err = f.Close(); err != nil {
			t.Fatalf("second File.Close: %v", err)
		}
		if err = e.c.Close(); err != nil {
			t.Fatal(err)
		}
		if err = e.c.Close(); err != nil {
			t.Fatalf("second Close: %v", err)
		}
		select {
		case <-e.c.Done():
		default:
			t.Fatal("Done not closed")
		}
		_, err = e.c.Stat(bg, e.rp("/f"))
		if errs.KindOf(err) != errs.ConnectionLost {
			t.Fatalf("Stat after Close: %v", err)
		}
	})
}
