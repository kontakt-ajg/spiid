package sftp

import (
	"context"
	"errors"
	"fmt"
	"io"

	"spiid/internal/errs"
)

const (
	readChunk   = 32768
	maxInFlight = 64
)

type inflight struct {
	off    int64
	length int
	id     uint32
	ch     chan reply
}

// ReadRange reads [off, end) with up to 64 outstanding 32 KiB READs, writes each reply with w.WriteAt at its
// offset, and calls progress(n) after each write (progress may be nil). EOF before end is errs.RemoteChanged;
// a write error is errs.LocalWrite; ctx cancel is errs.Canceled (the caller closes the connection to
// interrupt a blocked read).
func (f *File) ReadRange(ctx context.Context, off, end int64, w io.WriterAt, progress func(n int)) error {
	c := f.c
	if off < 0 {
		return errs.New(errs.InvalidInput, "read", f.path, errors.New("negative offset"))
	}
	var q []inflight
	defer func() {
		for _, r := range q {
			c.forget(r.id)
		}
	}()
	send := func(o int64, n int) error {
		id, ch, err := c.send(fxpRead, func(b []byte) []byte {
			b = appendString(b, f.handle)
			b = appendU64(b, uint64(o))
			return appendU32(b, uint32(n))
		})
		if err != nil {
			return err
		}
		q = append(q, inflight{off: o, length: n, id: id, ch: ch})
		return nil
	}
	next := off
	for {
		err := ctx.Err()
		if err != nil {
			return errs.New(errs.Canceled, "read", f.path, err)
		}
		for len(q) < maxInFlight && next < end {
			n := int(min(readChunk, end-next))
			err = send(next, n)
			if err != nil {
				return err
			}
			next += int64(n)
		}
		if len(q) == 0 {
			return nil
		}
		head := q[0]
		rep, err := c.wait(ctx, "read", f.path, head.id, head.ch)
		if err != nil {
			return err
		}
		q = q[1:]
		err = f.handleReply(head, rep, w, progress, func(o int64, n int) error {
			// the tail of a short reply is waited for next, so writes stay in increasing offset order
			err := send(o, n)
			if err == nil {
				last := q[len(q)-1]
				copy(q[1:], q[:len(q)-1])
				q[0] = last
			}
			return err
		})
		if err != nil {
			return err
		}
	}
}

// handleReply processes the reply of the oldest outstanding request.
func (f *File) handleReply(
	head inflight, rep reply, w io.WriterAt, progress func(int), send func(int64, int) error,
) error {
	c := f.c
	if rep.typ != fxpData {
		if isEOF(rep) {
			return errs.New(errs.RemoteChanged, "read", f.path, fmt.Errorf("file ended at %d", head.off))
		}
		return c.replyError("read", f.path, rep)
	}
	d := &decoder{b: rep.body}
	data := d.bytes()
	if d.err != nil {
		return c.badReply("read", f.path, d.err)
	}
	k := len(data)
	if k == 0 || k > head.length {
		return errs.New(errs.Protocol, "read", f.path, fmt.Errorf("DATA of %d bytes for a request of %d", k, head.length))
	}
	_, err := w.WriteAt(data, head.off)
	if err != nil {
		return errs.New(errs.LocalWrite, "read", f.path, err)
	}
	if progress != nil {
		progress(k)
	}
	if k < head.length {
		return send(head.off+int64(k), head.length-k)
	}
	return nil
}
