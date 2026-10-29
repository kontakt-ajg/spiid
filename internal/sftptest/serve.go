package sftptest

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"
)

// ServeOptions tunes ServeSFTP.
type ServeOptions struct {
	ReadDelay func() time.Duration // nil = none; called before every READ reply
	MaxRead   int                  // > 0: DATA replies carry at most MaxRead bytes (forces short reads)
	Done      <-chan struct{}      // closed = read delays end at once; nil = delays always run in full
}

// handle is an open file or folder.
type handle struct {
	f       *os.File
	dir     string
	entries []string // folder: names still to send
	isDir   bool
}

type session struct {
	rw      io.ReadWriteCloser
	root    string
	opts    ServeOptions
	handles map[string]*handle
	nextH   int
}

// ServeSFTP runs an SFTP v3 server over rw, rooted at root (paths are relative to root; "/" = root;
// ".." can't escape). Returns when rw fails or is closed. It does not import the client package: the server has
// its own codec, so client and server can't share a protocol bug unnoticed (and both are tested against OpenSSH).
func ServeSFTP(rw io.ReadWriteCloser, root string, opts ServeOptions) error {
	s := &session{rw: rw, root: root, opts: opts, handles: map[string]*handle{}}
	defer func() {
		for _, h := range s.handles {
			if h.f != nil {
				h.f.Close()
			}
		}
		rw.Close()
	}()
	typ, _, err := readPacket(rw)
	if err != nil {
		return err
	}
	if typ != tInit {
		return errors.New("sftptest: expected INIT")
	}
	err = s.write(newEnc(tVersion).u32(3))
	if err != nil {
		return err
	}
	// Requests are read ahead (like a socket buffer) but handled one at a time, in order.
	reqs := make(chan request, 256)
	stop := make(chan struct{})
	defer close(stop)
	var eof atomic.Bool
	go readRequests(rw, reqs, stop, &eof)
	for r := range reqs {
		if r.err != nil {
			return r.err
		}
		if eof.Load() {
			return io.EOF // the peer is gone: don't work through the queue
		}
		d := &dec{b: r.body}
		id := d.u32()
		if d.err != nil {
			return d.err
		}
		err = s.dispatch(r.typ, id, d)
		if err != nil {
			return err
		}
	}
	return nil
}

type request struct {
	typ  byte
	body []byte
	err  error
}

// readRequests reads packets until an error (delivered as the last request), or until stop is closed.
func readRequests(r io.Reader, out chan<- request, stop <-chan struct{}, eof *atomic.Bool) {
	defer close(out)
	for {
		typ, body, err := readPacket(r)
		if err != nil {
			eof.Store(true)
		}
		select {
		case out <- request{typ, body, err}:
		case <-stop:
			return
		}
		if err != nil {
			return
		}
	}
}

func (s *session) write(e *enc) error {
	_, err := s.rw.Write(e.packet())
	return err
}

func (s *session) status(id uint32, code uint32, msg string) error {
	return s.write(newEnc(tStatus).u32(id).u32(code).str(msg).str(""))
}

// fail replies with the status matching an OS error.
func (s *session) fail(id uint32, err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return s.status(id, sNoSuchFile, "no such file")
	case errors.Is(err, fs.ErrPermission):
		return s.status(id, sPermDenied, "permission denied")
	}
	return s.status(id, sFailure, err.Error())
}

// local maps a remote path to a real path under root.
func (s *session) local(p string) string {
	return filepath.Join(s.root, filepath.FromSlash(path.Clean("/"+p)))
}

func (s *session) dispatch(typ byte, id uint32, d *dec) error {
	switch typ {
	case tRealpath:
		return s.realpath(id, d)
	case tStat:
		return s.stat(id, d, os.Stat)
	case tLstat:
		return s.stat(id, d, os.Lstat)
	case tOpendir:
		return s.opendir(id, d)
	case tReaddir:
		return s.readdir(id, d)
	case tClose:
		return s.closeHandle(id, d)
	case tOpen:
		return s.open(id, d)
	case tRead:
		return s.read(id, d)
	}
	return s.status(id, sUnsupported, "operation unsupported")
}

func (s *session) newHandle(h *handle) string {
	s.nextH++
	name := strconv.Itoa(s.nextH)
	s.handles[name] = h
	return name
}
