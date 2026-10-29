package sftp

import (
	"context"
	"errors"
	"spiid/internal/errs"
	"sync"
	"time"
)

func pathRequest(p string) func([]byte) []byte {
	return func(b []byte) []byte {
		return appendString(b, p)
	}
}

// RealPath resolves p on the server.
func (c *Client) RealPath(ctx context.Context, p string) (string, error) {
	rep, err := c.do(ctx, "realpath", p, fxpRealpath, pathRequest(p))
	if err != nil {
		return "", err
	}
	if rep.typ != fxpName {
		return "", c.replyError("realpath", p, rep)
	}
	d := &decoder{b: rep.body}
	n := d.u32()
	var name string
	for i := uint32(0); i < n && d.err == nil; i++ {
		filename := d.str()
		d.str() // longname
		parseAttrs(d)
		if i == 0 {
			name = filename
		}
	}
	if d.err != nil || n == 0 || len(d.b) != 0 {
		return "", c.badReply("realpath", p, errors.New("bad NAME reply"))
	}
	return name, nil
}

// Stat returns information about p, following symbolic links.
func (c *Client) Stat(ctx context.Context, p string) (FileInfo, error) {
	return c.stat(ctx, "stat", fxpStat, p)
}

// Lstat returns information about p without following a final symbolic link.
func (c *Client) Lstat(ctx context.Context, p string) (FileInfo, error) {
	return c.stat(ctx, "lstat", fxpLstat, p)
}

func (c *Client) stat(ctx context.Context, op string, typ byte, p string) (FileInfo, error) {
	rep, err := c.do(ctx, op, p, typ, pathRequest(p))
	if err != nil {
		return FileInfo{}, err
	}
	if rep.typ != fxpAttrs {
		return FileInfo{}, c.replyError(op, p, rep)
	}
	d := &decoder{b: rep.body}
	a := parseAttrs(d)
	if d.err != nil {
		return FileInfo{}, c.badReply(op, p, d.err)
	}
	fi, err := a.fileInfo(baseName(p))
	if err != nil {
		return FileInfo{}, c.badReply(op, p, err)
	}
	return fi, nil
}

// openHandle sends OPENDIR or OPEN and returns the handle.
func (c *Client) openHandle(ctx context.Context, op, p string, typ byte, build func([]byte) []byte) (string, error) {
	rep, err := c.do(ctx, op, p, typ, build)
	if err != nil {
		return "", err
	}
	if rep.typ != fxpHandle {
		return "", c.replyError(op, p, rep)
	}
	d := &decoder{b: rep.body}
	h := d.str()
	if d.err != nil {
		return "", c.badReply(op, p, d.err)
	}
	return h, nil
}

// closeHandle sends CLOSE; it runs even when ctx is already canceled, but never longer than 10 s.
func (c *Client) closeHandle(ctx context.Context, p, handle string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	rep, err := c.do(ctx, "close", p, fxpClose, func(b []byte) []byte {
		return appendString(b, handle)
	})
	if err != nil {
		return err
	}
	if isStatusOK(rep) {
		return nil
	}
	return c.replyError("close", p, rep)
}

func isStatusOK(rep reply) bool {
	if rep.typ != fxpStatus {
		return false
	}
	code, _, err := parseStatus(rep.body)
	return err == nil && code == statusOK
}

// ReadDir lists folder p without "." and "..", in the order the server returns them.
func (c *Client) ReadDir(ctx context.Context, p string) ([]FileInfo, error) {
	h, err := c.openHandle(ctx, "opendir", p, fxpOpendir, pathRequest(p))
	if err != nil {
		return nil, err
	}
	list, err := c.readEntries(ctx, p, h)
	// Close the handle also after a listing error, unless the connection itself is gone.
	if c.brokenErr() == nil {
		if cerr := c.closeHandle(ctx, p, h); err == nil {
			err = cerr
		}
	}
	if err != nil {
		return nil, err
	}
	return list, nil
}

// A hostile server could send an endless listing; these bound the memory one listing can take.
const (
	maxDirEntries   = 1 << 20
	maxDirNameBytes = 64 << 20
)

func (c *Client) readEntries(ctx context.Context, p, h string) ([]FileInfo, error) {
	var list []FileInfo
	total := uint32(0)
	nameBytes := 0
	for {
		rep, err := c.do(ctx, "readdir", p, fxpReaddir, func(b []byte) []byte {
			return appendString(b, h)
		})
		if err != nil {
			return nil, err
		}
		if isEOF(rep) {
			return list, nil
		}
		if rep.typ != fxpName {
			return nil, c.replyError("readdir", p, rep)
		}
		d := &decoder{b: rep.body}
		n := d.u32()
		if n == 0 {
			err := errs.New(errs.Protocol, "readdir", p, errors.New("empty NAME reply"))
			c.fail(err)
			return nil, err
		}
		if n > maxDirEntries-total {
			err := errs.New(errs.Protocol, "readdir", p, errors.New("directory listing too large"))
			c.fail(err)
			return nil, err
		}
		total += n
		for i := uint32(0); i < n && d.err == nil; i++ {
			name := d.str()
			d.bytes() // longname
			a := parseAttrs(d)
			if d.err != nil {
				break
			}
			if name == "." || name == ".." {
				continue
			}
			if nameBytes += len(name); nameBytes > maxDirNameBytes {
				err := errs.New(errs.Protocol, "readdir", p, errors.New("directory listing too large"))
				c.fail(err)
				return nil, err
			}
			fi, err := a.fileInfo(name)
			if err != nil {
				return nil, c.badReply("readdir", p, err)
			}
			list = append(list, fi)
		}
		if d.err == nil && len(d.b) != 0 {
			d.err = errors.New("trailing NAME bytes")
		}
		if d.err != nil {
			return nil, c.badReply("readdir", p, d.err)
		}
	}
}

// File is a remote file opened for reading.
type File struct {
	c      *Client
	path   string
	handle string

	mu     sync.Mutex
	closed bool
}

// Open opens p read-only.
func (c *Client) Open(ctx context.Context, p string) (*File, error) {
	h, err := c.openHandle(ctx, "open", p, fxpOpen, func(b []byte) []byte {
		b = appendString(b, p)
		b = appendU32(b, pflagRead)
		return appendU32(b, 0)
	})
	if err != nil {
		return nil, err
	}
	return &File{c: c, path: p, handle: h}, nil
}

// Close releases the handle on the server. A second call does nothing and returns nil.
func (f *File) Close() error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil
	}
	f.closed = true
	f.mu.Unlock()
	return f.c.closeHandle(context.Background(), f.path, f.handle)
}
