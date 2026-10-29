package sftp_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"spiid/internal/errs"
	"spiid/internal/sftptest"
)

func TestReadRangeWhole(t *testing.T) {
	forEach(t, sftptest.Options{}, nil, func(t *testing.T, e *env) {
		data := writeRandomFile(t, filepath.Join(e.root, "big"), 5<<20)
		f, err := e.c.Open(bg, e.rp("/big"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		dst := newMemFile(len(data), 0)
		var sum int64
		err = f.ReadRange(bg, 0, int64(len(data)), dst, func(n int) { sum += int64(n) })
		if err != nil {
			t.Fatal(err)
		}
		if sha256.Sum256(dst.data) != sha256.Sum256(data) {
			t.Fatal("SHA-256 differs")
		}
		if sum != int64(len(data)) {
			t.Fatalf("progress sum %d, want %d", sum, len(data))
		}
	})
}

func TestReadRangeMiddle(t *testing.T) {
	forEach(t, sftptest.Options{}, nil, func(t *testing.T, e *env) {
		data := writeRandomFile(t, filepath.Join(e.root, "big"), 2<<20)
		f, err := e.c.Open(bg, e.rp("/big"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		const start, end = 100_000, 1_234_567
		dst := newMemFile(len(data), 0xAA)
		err = f.ReadRange(bg, start, end, dst, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dst.data[start:end], data[start:end]) {
			t.Fatal("range content differs")
		}
		for i, b := range dst.data {
			if (i < start || i >= end) && b != 0xAA {
				t.Fatalf("byte %d outside the range was written", i)
			}
		}
		if dst.Written() != end-start {
			t.Fatalf("wrote %d bytes, want %d", dst.Written(), end-start)
		}
	})
}

func TestReadRangeShortReads(t *testing.T) {
	forEach(t, sftptest.Options{MaxRead: 1000}, []string{"inmem", "ssh"}, func(t *testing.T, e *env) {
		data := writeRandomFile(t, filepath.Join(e.root, "f"), 300_000)
		f, err := e.c.Open(bg, e.rp("/f"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		dst := newMemFile(len(data), 0)
		var sum int64
		err = f.ReadRange(bg, 0, int64(len(data)), dst, func(n int) { sum += int64(n) })
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dst.data, data) || sum != int64(len(data)) {
			t.Fatalf("short reads: content equal=%v, progress %d", bytes.Equal(dst.data, data), sum)
		}
	})
}

func TestReadRangeShrunk(t *testing.T) {
	forEach(t, sftptest.Options{}, nil, func(t *testing.T, e *env) {
		p := filepath.Join(e.root, "f")
		writeRandomFile(t, p, 1<<20)
		f, err := e.c.Open(bg, e.rp("/f"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		err = os.Truncate(p, 100_000)
		if err != nil {
			t.Fatal(err)
		}
		err = f.ReadRange(bg, 0, 1<<20, newMemFile(1<<20, 0), nil)
		if errs.KindOf(err) != errs.RemoteChanged {
			t.Fatalf("got %v, want RemoteChanged", err)
		}
	})
}

func TestConnectionLostIsNotEOF(t *testing.T) {
	const size = 2 << 20
	forEach(t, sftptest.Options{ReadDelay: 20 * time.Millisecond}, []string{"ssh"}, func(t *testing.T, e *env) {
		writeRandomFile(t, filepath.Join(e.root, "f"), size)
		f, err := e.c.Open(bg, e.rp("/f"))
		if err != nil {
			t.Fatal(err)
		}
		dst := newMemFile(size, 0)
		var got atomic.Int64
		result := make(chan error, 1)
		go func() {
			result <- f.ReadRange(bg, 0, size, dst, func(n int) { got.Add(int64(n)) })
		}()
		deadline := time.Now().Add(5 * time.Second)
		for got.Load() == 0 {
			if time.Now().After(deadline) {
				t.Fatal("no progress before the drop")
			}
			time.Sleep(5 * time.Millisecond)
		}
		if w := dst.Written(); w <= 0 || w >= size {
			t.Fatalf("destination has %d of %d bytes before the drop; want a partial file", w, size)
		}
		e.srv.DropAll()
		select {
		case err = <-result:
		case <-time.After(10 * time.Second):
			t.Fatal("ReadRange did not return after DropAll")
		}
		if err == nil {
			t.Fatal("ReadRange reported success after the connection was dropped")
		}
		if errs.KindOf(err) != errs.ConnectionLost {
			t.Fatalf("kind %v (%v), want ConnectionLost", errs.KindOf(err), err)
		}
		select {
		case <-e.c.Done():
		case <-time.After(2 * time.Second):
			t.Fatal("Done not closed")
		}
		_, err = e.c.Stat(bg, "/f")
		if errs.KindOf(err) != errs.ConnectionLost {
			t.Fatalf("Stat after drop: %v", err)
		}
	})
}

func TestCancelReadRange(t *testing.T) {
	forEach(t, sftptest.Options{ReadDelay: 50 * time.Millisecond}, []string{"inmem", "ssh"}, func(t *testing.T, e *env) {
		writeRandomFile(t, filepath.Join(e.root, "f"), 2<<20)
		f, err := e.c.Open(bg, e.rp("/f"))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(bg)
		time.AfterFunc(100*time.Millisecond, cancel)
		start := time.Now()
		err = f.ReadRange(ctx, 0, 2<<20, newMemFile(2<<20, 0), nil)
		if errs.KindOf(err) != errs.Canceled {
			t.Fatalf("got %v, want Canceled", err)
		}
		if d := time.Since(start); d > time.Second {
			t.Fatalf("took %v to cancel", d)
		}
	})
}
