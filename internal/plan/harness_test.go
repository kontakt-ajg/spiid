package plan

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"spiid/internal/sftp"
	"spiid/internal/sftptest"
)

type pipeServer struct {
	io.Reader
	io.Writer
	r, w io.Closer
}

func (p pipeServer) Close() error { p.r.Close(); return p.w.Close() }

type testEnv struct {
	root, dl string
	c        *sftp.Client
	native   bool
}

func newEnv(t *testing.T, native bool) *testEnv {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &testEnv{root: root, dl: t.TempDir(), native: native}
	var reader io.Reader
	var writer io.WriteCloser
	if native {
		const binary = "/usr/libexec/sftp-server"
		if _, err := os.Stat(binary); err != nil {
			t.Skip(err)
		}
		cmd := exec.Command(binary)
		writer, err = cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		reader, err = cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err = cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	} else {
		r, w := io.Pipe()
		rr, ww := io.Pipe()
		done := make(chan struct{})
		go func() { defer close(done); sftptest.ServeSFTP(pipeServer{r, ww, r, ww}, root, sftptest.ServeOptions{}) }()
		reader, writer = rr, w
		t.Cleanup(func() { w.Close(); r.Close(); ww.Close(); rr.Close(); <-done })
	}
	e.c, err = sftp.NewClient(reader, writer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.c.Close() })
	return e
}

func (e *testEnv) remote(p string) string {
	if e.native {
		return filepath.ToSlash(filepath.Join(e.root, p))
	}
	return p
}

func (e *testEnv) file(t *testing.T, p string) {
	t.Helper()
	p = filepath.Join(e.root, p)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
}

func (e *testEnv) expand(t *testing.T, roots ...string) ([]Item, []Skipped) {
	t.Helper()
	for i := range roots {
		roots[i] = e.remote(roots[i])
	}
	items, skipped, err := Expand(context.Background(), e.c, roots, e.dl)
	if err != nil {
		t.Fatal(err)
	}
	return items, skipped
}

func both(t *testing.T, f func(*testing.T, *testEnv)) {
	for _, native := range []bool{false, true} {
		name := "inmem"
		if native {
			name = "openssh"
		}
		t.Run(name, func(t *testing.T) { f(t, newEnv(t, native)) })
	}
}
