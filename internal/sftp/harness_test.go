package sftp_test

import (
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"spiid/internal/sftp"
	"spiid/internal/sftptest"
)

const opensshServer = "/usr/libexec/sftp-server"

// env is one server flavour with a connected client.
type env struct {
	kind string
	root string // real folder served
	c    *sftp.Client
	srv  *sftptest.Server // only for "ssh"
}

// rp maps a remote path like "/x" to the path the server understands.
func (e *env) rp(p string) string {
	if e.kind == "openssh" {
		return filepath.Join(e.root, p)
	}
	return p
}

type rwc struct {
	io.Reader
	io.Writer
	closers []io.Closer
}

func (r rwc) Close() error {
	for _, c := range r.closers {
		c.Close()
	}
	return nil
}

// forEach runs fn on every server kind (or only the listed ones) as subtests inmem, ssh and openssh.
func forEach(t *testing.T, opts sftptest.Options, only []string, fn func(t *testing.T, e *env)) {
	t.Helper()
	for _, kind := range []string{"inmem", "ssh", "openssh"} {
		if len(only) > 0 && !contains(only, kind) {
			continue
		}
		t.Run(kind, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			e := &env{kind: kind, root: root}
			switch kind {
			case "inmem":
				e.startInmem(t, opts)
			case "ssh":
				e.startSSH(t, opts)
			case "openssh":
				e.startOpenSSH(t)
			}
			fn(t, e)
		})
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (e *env) startInmem(t *testing.T, opts sftptest.Options) {
	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	server := rwc{Reader: c2sR, Writer: s2cW, closers: []io.Closer{c2sR, s2cW}}
	so := sftptest.ServeOptions{MaxRead: opts.MaxRead}
	if opts.ReadDelay > 0 {
		so.ReadDelay = func() time.Duration { return opts.ReadDelay }
	}
	done := make(chan struct{})
	go func() {
		sftptest.ServeSFTP(server, e.root, so)
		close(done)
	}()
	c, err := sftp.NewClient(s2cR, c2sW)
	if err != nil {
		t.Fatal(err)
	}
	e.c = c
	t.Cleanup(func() {
		c.Close()
		<-done
	})
}

func (e *env) startSSH(t *testing.T, opts sftptest.Options) {
	opts.User, opts.Password = "u", "p"
	e.srv = sftptest.Start(t, e.root, opts)
	cfg := &ssh.ClientConfig{
		User:            "u",
		Auth:            []ssh.AuthMethod{ssh.Password("p")},
		HostKeyCallback: ssh.FixedHostKey(e.srv.HostKey()),
		Timeout:         5 * time.Second,
	}
	cl, err := ssh.Dial("tcp", net.JoinHostPort(e.srv.Host(), strconv.Itoa(e.srv.Port())), cfg)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := cl.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	err = sess.RequestSubsystem("sftp")
	if err != nil {
		t.Fatal(err)
	}
	c, err := sftp.NewClient(stdout, stdin)
	if err != nil {
		t.Fatal(err)
	}
	e.c = c
	t.Cleanup(func() {
		c.Close()
		sess.Close()
		cl.Close()
	})
}

func (e *env) startOpenSSH(t *testing.T) {
	_, err := os.Stat(opensshServer)
	if err != nil {
		t.Skipf("%s not available", opensshServer)
	}
	cmd := exec.Command(opensshServer)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	err = cmd.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	c, err := sftp.NewClient(stdout, stdin)
	if err != nil {
		t.Fatal(err)
	}
	e.c = c
	t.Cleanup(func() { c.Close() })
}

// memFile is an io.WriterAt over a byte slice that counts written bytes.
type memFile struct {
	mu      sync.Mutex
	data    []byte
	written int64
}

func newMemFile(size int, fill byte) *memFile {
	m := &memFile{data: make([]byte, size)}
	for i := range m.data {
		m.data[i] = fill
	}
	return m
}

func (m *memFile) WriteAt(p []byte, off int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	copy(m.data[off:], p)
	m.written += int64(len(p))
	return len(p), nil
}

func (m *memFile) Written() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.written
}

func writeRandomFile(t *testing.T, path string, size int) []byte {
	t.Helper()
	data := make([]byte, size)
	x := uint64(0x9E3779B97F4A7C15)
	for i := range data {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		data[i] = byte(x)
	}
	err := os.WriteFile(path, data, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
