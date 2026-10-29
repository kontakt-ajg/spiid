package sftptest

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"time"
)

const entriesPerReply = 50

func (s *session) realpath(id uint32, d *dec) error {
	p := d.str()
	if d.err != nil {
		return s.status(id, sBadMessage, "bad message")
	}
	clean := path.Clean("/" + p)
	return s.write(newEnc(tName).u32(id).u32(1).str(clean).str(clean).emptyAttrs())
}

func (s *session) stat(id uint32, d *dec, do func(string) (os.FileInfo, error)) error {
	p := d.str()
	if d.err != nil {
		return s.status(id, sBadMessage, "bad message")
	}
	fi, err := do(s.local(p))
	if err != nil {
		return s.fail(id, err)
	}
	return s.write(newEnc(tAttrs).u32(id).attrs(fi))
}

func (s *session) opendir(id uint32, d *dec) error {
	p := d.str()
	if d.err != nil {
		return s.status(id, sBadMessage, "bad message")
	}
	dir := s.local(p)
	list, err := os.ReadDir(dir)
	if err != nil {
		return s.fail(id, err)
	}
	h := &handle{isDir: true, dir: dir}
	for _, e := range list {
		h.entries = append(h.entries, e.Name())
	}
	return s.write(newEnc(tHandle).u32(id).str(s.newHandle(h)))
}

func (s *session) readdir(id uint32, d *dec) error {
	h := s.handles[d.str()]
	if d.err != nil || h == nil || !h.isDir {
		return s.status(id, sFailure, "bad handle")
	}
	if len(h.entries) == 0 {
		return s.status(id, sEOF, "EOF")
	}
	n := min(entriesPerReply, len(h.entries))
	e := newEnc(tName).u32(id)
	var body []byte
	count := 0
	for _, name := range h.entries[:n] {
		fi, err := os.Lstat(filepath.Join(h.dir, name))
		if err != nil {
			continue // vanished meanwhile
		}
		long := fmt.Sprintf("%s 1 user group %d Jan  1 00:00 %s", fi.Mode().String(), fi.Size(), name)
		item := newEnc(0).str(name).str(long).attrs(fi)
		body = append(body, item.b[5:]...) // skip length and the dummy type byte
		count++
	}
	h.entries = h.entries[n:]
	e.u32(uint32(count))
	e.b = append(e.b, body...)
	return s.write(e)
}

func (s *session) closeHandle(id uint32, d *dec) error {
	name := d.str()
	h := s.handles[name]
	if d.err != nil || h == nil {
		return s.status(id, sFailure, "bad handle")
	}
	delete(s.handles, name)
	if h.f != nil {
		h.f.Close()
	}
	return s.status(id, sOK, "OK")
}

func (s *session) open(id uint32, d *dec) error {
	p := d.str()
	flags := d.u32()
	if d.err != nil {
		return s.status(id, sBadMessage, "bad message")
	}
	if flags != 0x1 {
		return s.status(id, sPermDenied, "only read access")
	}
	f, err := os.Open(s.local(p))
	if err != nil {
		return s.fail(id, err)
	}
	return s.write(newEnc(tHandle).u32(id).str(s.newHandle(&handle{f: f})))
}

func (s *session) read(id uint32, d *dec) error {
	h := s.handles[d.str()]
	off := d.u64()
	n := d.u32()
	if d.err != nil || h == nil || h.f == nil {
		return s.status(id, sFailure, "bad handle")
	}
	size := min(int(min(n, maxPacket-1024)), 256*1024)
	if s.opts.MaxRead > 0 {
		size = min(size, s.opts.MaxRead)
	}
	buf := make([]byte, size)
	got, err := h.f.ReadAt(buf, int64(off))
	if got == 0 && size > 0 {
		if err == io.EOF {
			s.delay()
			return s.status(id, sEOF, "EOF")
		}
		if err != nil {
			return s.fail(id, err)
		}
	}
	s.delay()
	return s.write(newEnc(tData).u32(id).bytes(buf[:got]))
}

func (s *session) delay() {
	if s.opts.ReadDelay == nil {
		return
	}
	d := s.opts.ReadDelay()
	if d > 0 {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-s.opts.Done:
		}
	}
}
