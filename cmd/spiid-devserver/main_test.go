package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"spiid/internal/sftp"
)

// syncBuf is a bytes.Buffer safe for use by run (writer) and the test (reader).
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

var readyRE = regexp.MustCompile(`^READY addr=(\S+) user=(\S+) fingerprint=(SHA256:\S+)\n$`)

// start runs the command and returns the READY fields, a stop function and the channel with run's result.
func start(t *testing.T, args ...string) (addr, user, fp string, stop func(), done chan error) {
	t.Helper()
	out := &syncBuf{}
	stopCh := make(chan struct{})
	done = make(chan error, 1)
	go func() { done <- run(args, out, stopCh) }()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.HasSuffix(out.String(), "\n") {
		select {
		case err := <-done:
			t.Fatalf("run returned early: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("no READY line")
		}
		time.Sleep(5 * time.Millisecond)
	}
	m := readyRE.FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("bad READY line %q", out.String())
	}
	var once sync.Once
	stop = func() { once.Do(func() { close(stopCh) }) }
	t.Cleanup(stop)
	return m[1], m[2], m[3], stop, done
}

func TestServeAndStop(t *testing.T) {
	root := t.TempDir()
	err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	addr, user, fp, stop, done := start(t, "-root", root, "-addr", "127.0.0.1:0")
	if user != "dev" {
		t.Fatalf("user = %q", user)
	}
	var seen string
	cfg := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{ssh.Password("dev")},
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			seen = ssh.FingerprintSHA256(k)
			return nil
		},
		Timeout: 5 * time.Second,
	}
	c, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if seen != fp {
		t.Fatalf("fingerprint %q, READY said %q", seen, fp)
	}
	sess, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	w, err := sess.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	r, err := sess.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	err = sess.RequestSubsystem("sftp")
	if err != nil {
		t.Fatal(err)
	}
	cl, err := sftp.NewClient(r, w)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := cl.Stat(context.Background(), "/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size != 5 {
		t.Fatalf("size = %d, want 5", fi.Size)
	}
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run after stop: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after stop")
	}
}

func TestNonLoopbackRefused(t *testing.T) {
	for _, a := range []string{"0.0.0.0:0", "192.168.1.5:2222", "example.com:22", ":2222", "nonsense"} {
		err := run([]string{"-root", t.TempDir(), "-addr", a}, io.Discard, make(chan struct{}))
		if err == nil {
			t.Errorf("-addr %q was accepted", a)
		}
	}
}

func TestRootRequired(t *testing.T) {
	err := run(nil, io.Discard, make(chan struct{}))
	if err == nil {
		t.Fatal("missing -root was accepted")
	}
}

func TestKnownHostsFile(t *testing.T) {
	kh := filepath.Join(t.TempDir(), "known_hosts")
	err := os.WriteFile(kh, []byte("old\n"), 0o644) // existing file: mode must still end up 0600
	if err != nil {
		t.Fatal(err)
	}
	addr, _, _, stop, done := start(t, "-root", t.TempDir(), "-addr", "127.0.0.1:0", "-known-hosts", kh)
	fi, err := os.Stat(kh)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("known_hosts mode = %v", fi.Mode().Perm())
	}
	cb, err := knownhosts.New(kh)
	if err != nil {
		t.Fatalf("knownhosts.New: %v", err)
	}
	c, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            "dev",
		Auth:            []ssh.AuthMethod{ssh.Password("dev")},
		HostKeyCallback: cb,
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial with known_hosts: %v", err)
	}
	c.Close()
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestMakeSample(t *testing.T) {
	root := t.TempDir()
	err := makeSample(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(root, "sample-0.bin"))
	if err != nil || fi.Size() != sampleSize {
		t.Fatalf("sample-0.bin: %v, %v", fi, err)
	}
	count := 0
	err = filepath.WalkDir(filepath.Join(root, "tree"), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			count++
		}
		return err
	})
	if err != nil || count != 60 {
		t.Fatalf("tree files = %d, %v", count, err)
	}
	// Existing files are skipped, not rewritten.
	err = os.WriteFile(filepath.Join(root, "tree", "file-00.txt"), []byte("keep"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	err = makeSample(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, "tree", "file-00.txt"))
	if string(b) != "keep" {
		t.Fatalf("existing file was rewritten: %q", b)
	}
}

type discardAt struct{}

func (discardAt) WriteAt(p []byte, _ int64) (int, error) { return len(p), nil }

// Stopping must not wait for an in-flight read delay.
func TestShutdownCancelsDelayedRead(t *testing.T) {
	root := t.TempDir()
	err := os.WriteFile(filepath.Join(root, "a"), []byte("hello"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	addr, _, _, stop, done := start(t, "-root", root, "-addr", "127.0.0.1:0", "-read-delay", "30s")
	c, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{User: "dev", Auth: []ssh.AuthMethod{ssh.Password("dev")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sess, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	w, _ := sess.StdinPipe()
	r, _ := sess.StdoutPipe()
	err = sess.RequestSubsystem("sftp")
	if err != nil {
		t.Fatal(err)
	}
	cl, err := sftp.NewClient(r, w)
	if err != nil {
		t.Fatal(err)
	}
	f, err := cl.Open(context.Background(), "/a")
	if err != nil {
		t.Fatal(err)
	}
	readDone := make(chan error, 1)
	go func() { readDone <- f.ReadRange(context.Background(), 0, 5, discardAt{}, nil) }()
	select { // the state must really be "read in flight"
	case err := <-readDone:
		t.Fatalf("read did not stay pending: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	began := time.Now()
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
		if d := time.Since(began); d > 5*time.Second {
			t.Errorf("shutdown took %v", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown blocked on the read delay")
	}
	select {
	case <-readDone:
	case <-time.After(5 * time.Second):
		t.Fatal("read still pending after shutdown")
	}
}

// Only literal loopback IPs are accepted; names (even localhost) are not.
func TestListenRequiresLiteralLoopback(t *testing.T) {
	for _, a := range []string{"localhost:0", "LOCALHOST:0", "localhost.localdomain:0", "[::]:0", "10.0.0.1:0"} {
		err := run([]string{"-root", t.TempDir(), "-addr", a}, io.Discard, make(chan struct{}))
		if err == nil {
			t.Errorf("-addr %q was accepted", a)
		}
	}
	for _, a := range []string{"127.0.0.1:0", "127.0.0.2:0", "[::1]:0"} {
		if err := checkLoopback(a); err != nil {
			t.Errorf("-addr %q refused: %v", a, err)
		}
	}
}
