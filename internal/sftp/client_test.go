package sftp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"spiid/internal/errs"
	"spiid/internal/sftptest"
)

func probeClient(t *testing.T, fn func(io.Reader, io.Writer)) *Client {
	t.Helper()
	r, w := fakeServer(t, fn)
	c, err := NewClient(r, w)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func probeID(b []byte) uint32 { return binary.BigEndian.Uint32(b) }

func TestRealPathRejectsTruncatedName(t *testing.T) {
	hold := make(chan struct{})
	defer close(hold)
	c := probeClient(t, func(r io.Reader, w io.Writer) {
		_, b, _ := readPacket(r)
		p := appendU32(newPacket(fxpName, probeID(b)), 1)
		p = appendString(p, "/forged")
		w.Write(finish(p))
		<-hold
	})
	got, err := c.RealPath(context.Background(), "/")
	if err == nil {
		t.Fatalf("truncated NAME (no longname or ATTRS) accepted: %q; Done open", got)
	}
}

func TestStatRejectsTruncatedStatus(t *testing.T) {
	hold := make(chan struct{})
	defer close(hold)
	c := probeClient(t, func(r io.Reader, w io.Writer) {
		_, b, _ := readPacket(r)
		p := appendU32(newPacket(fxpStatus, probeID(b)), statusNoSuchFile)
		p = appendU32(p, 99)
		p = append(p, 'x')
		w.Write(finish(p))
		<-hold
	})
	_, err := c.Stat(context.Background(), "/x")
	if errs.KindOf(err) != errs.ConnectionLost {
		t.Fatalf("truncated STATUS returned %v; want ConnectionLost", err)
	}
}

func TestStatReportsMalformedAttrsAsConnectionLost(t *testing.T) {
	hold := make(chan struct{})
	defer close(hold)
	c := probeClient(t, func(r io.Reader, w io.Writer) {
		_, b, _ := readPacket(r)
		w.Write(finish(newPacket(fxpAttrs, probeID(b))))
		<-hold
	})
	_, err := c.Stat(context.Background(), "/x")
	if errs.KindOf(err) != errs.ConnectionLost {
		t.Fatalf("undecodable ATTRS returned kind %v (%v); want ConnectionLost", errs.KindOf(err), err)
	}
}

func TestDuplicateResponseBreaksConnection(t *testing.T) {
	c := probeClient(t, func(r io.Reader, w io.Writer) {
		_, b, _ := readPacket(r)
		p := finish(appendU32(newPacket(fxpAttrs, probeID(b)), 0))
		w.Write(p)
		w.Write(p)
		_, b, err := readPacket(r)
		if err != nil {
			return
		}
		w.Write(finish(appendU32(newPacket(fxpAttrs, probeID(b)), 0)))
	})
	if _, err := c.Stat(context.Background(), "/one"); err != nil {
		t.Fatal(err)
	}
	_, err := c.Stat(context.Background(), "/two")
	if errs.KindOf(err) != errs.ConnectionLost {
		t.Fatalf("duplicate completed id ignored; following Stat returned %v", err)
	}
}

func TestFileCloseReportsBrokenConnection(t *testing.T) {
	hold := make(chan struct{})
	defer close(hold)
	c := probeClient(t, func(r io.Reader, w io.Writer) {
		_, b, _ := readPacket(r)
		w.Write(finish(appendString(newPacket(fxpHandle, probeID(b)), "handle")))
		<-hold
	})
	f, err := c.Open(context.Background(), "/x")
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if err := f.Close(); errs.KindOf(err) != errs.ConnectionLost {
		t.Fatalf("first File.Close on broken connection returned %v; want ConnectionLost", err)
	}
}

func TestCanceledRequestsBoundTombstones(t *testing.T) {
	c := probeClient(t, func(r io.Reader, w io.Writer) {
		for {
			if _, _, err := readPacket(r); err != nil {
				return
			}
		}
	})
	for i := 0; i < maxTombstones; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c.forget(func() uint32 {
			id, _, err := c.send(fxpStat, func(b []byte) []byte { return appendString(b, "/x") })
			if err != nil {
				t.Fatal(err)
			}
			return id
		}())
		_ = ctx
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("client not broken after maxTombstones unanswered canceled requests")
	}
	c.mu.Lock()
	n, left := c.tombstones, len(c.pending)
	c.mu.Unlock()
	if n != 0 || left != 0 {
		t.Fatalf("after break: tombstones=%d pending=%d", n, left)
	}
}

type writeRec struct{ off, n int64 }

// recWriter records every WriteAt and keeps the content.
type recWriter struct {
	mu     sync.Mutex
	data   []byte
	writes []writeRec
}

func (w *recWriter) WriteAt(p []byte, off int64) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes = append(w.writes, writeRec{off, int64(len(p))})
	copy(w.data[off:], p)
	return len(p), nil
}

type pipeRWC struct {
	io.Reader
	io.Writer
	closers []io.Closer
}

func (r pipeRWC) Close() error {
	for _, c := range r.closers {
		c.Close()
	}
	return nil
}

func TestClientWritesRequestsInOrder(t *testing.T) {
	root := t.TempDir()
	data := make([]byte, 1<<20)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "f"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	server := pipeRWC{Reader: c2sR, Writer: s2cW, closers: []io.Closer{c2sR, s2cW}}
	done := make(chan struct{})
	go func() {
		sftptest.ServeSFTP(server, root, sftptest.ServeOptions{MaxRead: 1000})
		close(done)
	}()
	c, err := NewClient(s2cR, c2sW)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		c.Close()
		<-done
	}()
	f, err := c.Open(context.Background(), "/f")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := &recWriter{data: make([]byte, len(data))}
	if err := f.ReadRange(context.Background(), 0, int64(len(data)), w, nil); err != nil {
		t.Fatal(err)
	}
	next := int64(0)
	for i, wr := range w.writes {
		if wr.off != next {
			t.Fatalf("write %d starts at %d, want %d", i, wr.off, next)
		}
		next += wr.n
	}
	if next != int64(len(data)) {
		t.Fatalf("wrote %d bytes, want %d", next, len(data))
	}
	if len(w.writes) < 1000 {
		t.Fatalf("only %d writes: short reads were not exercised", len(w.writes))
	}
	if !bytes.Equal(w.data, data) {
		t.Fatal("content differs")
	}
}

// slowWriter is a WriteCloser whose Write takes 50 ms and which records whether Close overlapped a Write.
type slowWriter struct {
	inWrite    atomic.Bool
	overlapped atomic.Bool
	closed     atomic.Bool
	version    io.Writer
	writes     atomic.Int32
}

func (s *slowWriter) Write(b []byte) (int, error) {
	if s.writes.Add(1) == 1 { // INIT: answer by hand
		go s.version.Write([]byte{0, 0, 0, 5, fxpVersion, 0, 0, 0, 3})
		return len(b), nil
	}
	s.inWrite.Store(true)
	time.Sleep(50 * time.Millisecond)
	s.inWrite.Store(false)
	return len(b), nil
}

func (s *slowWriter) Close() error {
	if s.inWrite.Load() {
		s.overlapped.Store(true)
	}
	s.closed.Store(true)
	return nil
}

func TestCloseWaitsForInFlightWrite(t *testing.T) {
	s2cR, s2cW := io.Pipe()
	defer s2cW.Close()
	w := &slowWriter{version: s2cW}
	c, err := NewClient(s2cR, w)
	if err != nil {
		t.Fatal(err)
	}
	go c.Stat(context.Background(), "/x")
	deadline := time.Now().Add(time.Second)
	for !w.inWrite.Load() {
		if time.Now().After(deadline) {
			t.Fatal("write never started")
		}
		time.Sleep(time.Millisecond)
	}
	c.Close()
	if w.closed.Load() {
		t.Fatal("writer closed while a write was still in progress")
	}
	for !w.closed.Load() {
		if time.Now().After(deadline) {
			t.Fatal("writer never closed")
		}
		time.Sleep(time.Millisecond)
	}
	if w.overlapped.Load() {
		t.Fatal("Close overlapped a Write")
	}
}
