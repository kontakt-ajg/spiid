// Package sftptest is an in-process SFTP v3 server and SSH server for tests. It has its own packet codec and
// does not import the client package.
package sftptest

import (
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
)

// Packet types the server understands or sends.
const (
	tInit     = 1
	tVersion  = 2
	tOpen     = 3
	tClose    = 4
	tRead     = 5
	tLstat    = 7
	tOpendir  = 11
	tReaddir  = 12
	tRealpath = 16
	tStat     = 17
	tStatus   = 101
	tHandle   = 102
	tData     = 103
	tName     = 104
	tAttrs    = 105
)

// Status codes.
const (
	sOK          = 0
	sEOF         = 1
	sNoSuchFile  = 2
	sPermDenied  = 3
	sFailure     = 4
	sBadMessage  = 5
	sUnsupported = 8
)

const maxPacket = 256*1024 + 1024

var errShort = errors.New("sftptest: short packet")

func readPacket(r io.Reader) (byte, []byte, error) {
	var head [4]byte
	_, err := io.ReadFull(r, head[:])
	if err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(head[:])
	if n == 0 || n > maxPacket {
		return 0, nil, errors.New("sftptest: bad packet length")
	}
	buf := make([]byte, n)
	_, err = io.ReadFull(r, buf)
	if err != nil {
		return 0, nil, err
	}
	return buf[0], buf[1:], nil
}

// enc builds one packet.
type enc struct{ b []byte }

func newEnc(typ byte) *enc {
	return &enc{b: append(make([]byte, 4, 64), typ)}
}

func (e *enc) u32(v uint32) *enc {
	e.b = binary.BigEndian.AppendUint32(e.b, v)
	return e
}

func (e *enc) u64(v uint64) *enc {
	e.b = binary.BigEndian.AppendUint64(e.b, v)
	return e
}

func (e *enc) str(s string) *enc {
	e.u32(uint32(len(s)))
	e.b = append(e.b, s...)
	return e
}

func (e *enc) bytes(p []byte) *enc {
	e.u32(uint32(len(p)))
	e.b = append(e.b, p...)
	return e
}

// attrs appends ATTRS with size, permissions and times.
func (e *enc) attrs(fi fs.FileInfo) *enc {
	e.u32(0x1 | 0x4 | 0x8)
	e.u64(uint64(fi.Size()))
	e.u32(permBits(fi.Mode()))
	t := uint32(fi.ModTime().Unix())
	e.u32(t)
	return e.u32(t)
}

func (e *enc) emptyAttrs() *enc {
	return e.u32(0)
}

func (e *enc) packet() []byte {
	binary.BigEndian.PutUint32(e.b, uint32(len(e.b)-4))
	return e.b
}

// permBits converts a Go mode to POSIX type and permission bits.
func permBits(m fs.FileMode) uint32 {
	p := uint32(m.Perm())
	switch {
	case m.IsDir():
		p |= 0o040000
	case m&fs.ModeSymlink != 0:
		p |= 0o120000
	case m&fs.ModeNamedPipe != 0:
		p |= 0o010000
	case m&fs.ModeSocket != 0:
		p |= 0o140000
	case m&fs.ModeCharDevice != 0:
		p |= 0o020000
	case m&fs.ModeDevice != 0:
		p |= 0o060000
	default:
		p |= 0o100000
	}
	return p
}

// dec reads from a packet body.
type dec struct {
	b   []byte
	err error
}

func (d *dec) u32() uint32 {
	if len(d.b) < 4 {
		d.err = errShort
		return 0
	}
	v := binary.BigEndian.Uint32(d.b)
	d.b = d.b[4:]
	return v
}

func (d *dec) u64() uint64 {
	if len(d.b) < 8 {
		d.err = errShort
		return 0
	}
	v := binary.BigEndian.Uint64(d.b)
	d.b = d.b[8:]
	return v
}

func (d *dec) str() string {
	n := d.u32()
	if d.err != nil || uint64(n) > uint64(len(d.b)) {
		d.err = errShort
		return ""
	}
	s := string(d.b[:n])
	d.b = d.b[n:]
	return s
}
