package sftptest

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

type pipeRW struct {
	io.Reader
	io.Writer
	closers []io.Closer
}

func (p pipeRW) Close() error {
	for _, c := range p.closers {
		c.Close()
	}
	return nil
}

// rawClient speaks the protocol with the test codec.
type rawClient struct {
	t  *testing.T
	rw io.ReadWriteCloser
	id uint32
}

func newRawClient(t *testing.T, root string) *rawClient {
	t.Helper()
	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	server := pipeRW{Reader: c2sR, Writer: s2cW, closers: []io.Closer{c2sR, s2cW}}
	done := make(chan struct{})
	go func() {
		ServeSFTP(server, root, ServeOptions{})
		close(done)
	}()
	client := pipeRW{Reader: s2cR, Writer: c2sW, closers: []io.Closer{c2sW, s2cR}}
	t.Cleanup(func() {
		client.Close()
		<-done
	})
	rc := &rawClient{t: t, rw: client}
	_, err := client.Write(newEnc(tInit).u32(3).packet())
	if err != nil {
		t.Fatal(err)
	}
	typ, body, err := readPacket(client)
	if err != nil || typ != tVersion || (&dec{b: body}).u32() != 3 {
		t.Fatalf("bad VERSION: %d %v", typ, err)
	}
	return rc
}

// call sends a request whose payload is built by fill and returns the reply type and body after the id.
func (c *rawClient) call(typ byte, fill func(e *enc)) (byte, *dec) {
	c.t.Helper()
	c.id++
	e := newEnc(typ).u32(c.id)
	fill(e)
	_, err := c.rw.Write(e.packet())
	if err != nil {
		c.t.Fatal(err)
	}
	rt, body, err := readPacket(c.rw)
	if err != nil {
		c.t.Fatal(err)
	}
	d := &dec{b: body}
	if got := d.u32(); got != c.id {
		c.t.Fatalf("reply id %d, want %d", got, c.id)
	}
	return rt, d
}

func TestPathEscape(t *testing.T) {
	root := t.TempDir()
	c := newRawClient(t, root)
	typ, d := c.call(tRealpath, func(e *enc) { e.str("/../../etc") })
	if typ != tName {
		t.Fatalf("REALPATH reply type %d", typ)
	}
	d.u32()
	if got := d.str(); got != "/etc" {
		t.Fatalf("REALPATH = %q, want /etc", got)
	}
	// "/etc" under root does not exist; the real /etc must not be reached.
	typ, d = c.call(tStat, func(e *enc) { e.str("/../../etc") })
	if typ != tStatus || d.u32() != sNoSuchFile {
		t.Fatalf("STAT escaped the root (reply type %d)", typ)
	}
	err := os.Mkdir(filepath.Join(root, "etc"), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	typ, _ = c.call(tStat, func(e *enc) { e.str("/../../etc") })
	if typ != tAttrs {
		t.Fatalf("STAT of root/etc: reply type %d", typ)
	}
	typ, d = c.call(tRealpath, func(e *enc) { e.str("") })
	d.u32()
	if got := d.str(); typ != tName || got != "/" {
		t.Fatalf("REALPATH of empty = %q", got)
	}
}

func TestUnsupportedOp(t *testing.T) {
	c := newRawClient(t, t.TempDir())
	const mkdir = 14
	typ, d := c.call(mkdir, func(e *enc) { e.str("/x").u32(0) })
	if typ != tStatus || d.u32() != sUnsupported {
		t.Fatalf("MKDIR: reply type %d, want STATUS OP_UNSUPPORTED", typ)
	}
}
