package sftp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"spiid/internal/errs"
)

// maxTombstones is how many canceled, still unanswered requests a client keeps track of before it gives up on
// the connection.
const maxTombstones = 4096

// reply is a decoded packet header: type and the body after the request id.
type reply struct {
	typ  byte
	body []byte
}

// Client is an SFTP v3 client. It is safe for concurrent use.
type Client struct {
	r io.Reader
	w io.WriteCloser

	wmu sync.Mutex // serializes writes

	mu         sync.Mutex
	pending    map[uint32]chan reply
	tombstones int // entries in pending that are nil (canceled, reply not yet seen)
	lastID     uint32
	broken     error
	done       chan struct{}
}

// NewClient sends INIT(3) on w and waits for VERSION on r. Closing the client closes w.
func NewClient(r io.Reader, w io.WriteCloser) (*Client, error) {
	c := &Client{r: r, w: w, pending: map[uint32]chan reply{}, done: make(chan struct{})}
	err := c.handshake()
	if err != nil {
		w.Close()
		return nil, err
	}
	go c.readLoop()
	return c, nil
}

func (c *Client) handshake() error {
	b := make([]byte, 4, 9)
	b = append(b, fxpInit)
	b = appendU32(b, protocolVersion)
	_, err := c.w.Write(finish(b))
	if err != nil {
		return errs.New(errs.ConnectionLost, "sftp", "", err)
	}
	typ, body, err := readPacket(c.r)
	if err != nil {
		if errs.KindOf(err) == errs.Protocol {
			return err
		}
		return errs.New(errs.ConnectionLost, "sftp", "", err)
	}
	if typ != fxpVersion {
		return errs.New(errs.Protocol, "sftp", "", fmt.Errorf("expected VERSION, got packet type %d", typ))
	}
	d := &decoder{b: body}
	v := d.u32()
	if d.err != nil {
		return errs.New(errs.Protocol, "sftp", "", d.err)
	}
	if v != protocolVersion {
		return errs.New(errs.Protocol, "sftp", "", fmt.Errorf("unsupported SFTP version %d", v))
	}
	return nil
}

// fail marks the client broken, once. It reports whether this call was the first; w is closed in the background.
func (c *Client) fail(cause error) (bool, error) {
	c.mu.Lock()
	if c.broken != nil {
		c.mu.Unlock()
		return false, nil
	}
	c.broken = errs.New(errs.ConnectionLost, "sftp", "", cause)
	clear(c.pending)
	c.tombstones = 0
	close(c.done)
	c.mu.Unlock()
	go c.closeWriter()
	return true, nil
}

// closeWriter closes w once no write is in progress (writes hold wmu). A write blocked on a dead transport is
// released when the transport itself is closed (sshconn closes the SSH connection first).
func (c *Client) closeWriter() {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.w.Close()
}

func (c *Client) brokenErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.broken
}

// Close marks the client closed and closes w as soon as no write is in progress. It is
// idempotent and returns nil.
func (c *Client) Close() error {
	c.fail(errors.New("client closed"))
	return nil
}

// Done is closed when the transport fails or Close is called.
func (c *Client) Done() <-chan struct{} {
	return c.done
}

// readLoop is the only reader: it hands every reply to the request waiting for its id.
func (c *Client) readLoop() {
	for {
		typ, body, err := readPacket(c.r)
		if err != nil {
			c.fail(err)
			return
		}
		if typ < fxpStatus || typ > fxpAttrs || len(body) < 4 {
			c.fail(errs.New(errs.Protocol, "sftp", "", fmt.Errorf("bad reply: type %d, %d bytes", typ, len(body))))
			return
		}
		id := uint32(body[0])<<24 | uint32(body[1])<<16 | uint32(body[2])<<8 | uint32(body[3])
		c.mu.Lock()
		ch, ok := c.pending[id]
		if ok {
			delete(c.pending, id)
			if ch == nil {
				c.tombstones--
			}
		}
		c.mu.Unlock()
		if !ok {
			c.fail(errs.New(errs.Protocol, "sftp", "", fmt.Errorf("reply with unknown id %d", id)))
			return
		}
		if ch != nil {
			ch <- reply{typ: typ, body: body[4:]}
		}
	}
}

// send registers a request and writes it; build appends the payload after the id.
func (c *Client) send(typ byte, build func([]byte) []byte) (uint32, chan reply, error) {
	c.mu.Lock()
	if c.broken != nil {
		err := c.broken
		c.mu.Unlock()
		return 0, nil, err
	}
	c.lastID++
	id := c.lastID
	ch := make(chan reply, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	pkt := finish(build(newPacket(typ, id)))
	c.wmu.Lock()
	err := c.brokenErr()
	if err != nil {
		c.wmu.Unlock()
		c.forget(id)
		return 0, nil, err
	}
	_, err = c.w.Write(pkt)
	c.wmu.Unlock()
	if err != nil {
		c.fail(err)
		c.forget(id)
		return 0, nil, c.brokenErr()
	}
	return id, ch, nil
}

// forget abandons request id: its entry becomes a tombstone so that a late reply is dropped, not treated as an
// unknown id. Too many tombstones mean the server stopped answering: the client is broken.
func (c *Client) forget(id uint32) {
	c.mu.Lock()
	full := false
	if ch, ok := c.pending[id]; ok && ch != nil {
		c.pending[id] = nil
		c.tombstones++
		full = c.tombstones >= maxTombstones
	}
	c.mu.Unlock()
	if full {
		c.fail(errors.New("too many unanswered canceled requests"))
	}
}

// wait blocks for the reply to request id.
func (c *Client) wait(ctx context.Context, op, path string, id uint32, ch chan reply) (reply, error) {
	select {
	case r := <-ch:
		return r, nil
	case <-ctx.Done():
		c.forget(id)
		return reply{}, errs.New(errs.Canceled, op, path, ctx.Err())
	case <-c.done:
		select {
		case r := <-ch:
			return r, nil
		default:
		}
		c.forget(id)
		return reply{}, c.brokenErr()
	}
}

// do sends one request and waits for its reply.
func (c *Client) do(ctx context.Context, op, path string, typ byte, build func([]byte) []byte) (reply, error) {
	err := ctx.Err()
	if err != nil {
		return reply{}, errs.New(errs.Canceled, op, path, err)
	}
	id, ch, err := c.send(typ, build)
	if err != nil {
		return reply{}, err
	}
	return c.wait(ctx, op, path, id, ch)
}
