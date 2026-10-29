// Package sftp is a small SFTP v3 client (draft-ietf-secsh-filexfer-02) with pipelined reads.
package sftp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"spiid/internal/errs"
)

// Packet types.
const (
	fxpInit     = 1
	fxpVersion  = 2
	fxpOpen     = 3
	fxpClose    = 4
	fxpRead     = 5
	fxpLstat    = 7
	fxpOpendir  = 11
	fxpReaddir  = 12
	fxpRealpath = 16
	fxpStat     = 17
	fxpStatus   = 101
	fxpHandle   = 102
	fxpData     = 103
	fxpName     = 104
	fxpAttrs    = 105
)

// STATUS codes.
const (
	statusOK            = 0
	statusEOF           = 1
	statusNoSuchFile    = 2
	statusPermDenied    = 3
	statusOpUnsupported = 8
)

const (
	protocolVersion = 3
	pflagRead       = 0x1
	maxPacketSize   = 256*1024 + 1024
)

var errShort = errors.New("short packet")

// readPacket reads one packet and returns its type and payload. A length of 0 or above maxPacketSize is
// rejected before anything is allocated.
func readPacket(r io.Reader) (byte, []byte, error) {
	var head [4]byte
	_, err := io.ReadFull(r, head[:])
	if err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(head[:])
	if n == 0 || n > maxPacketSize {
		return 0, nil, errs.New(errs.Protocol, "sftp", "", fmt.Errorf("bad packet length %d", n))
	}
	buf := make([]byte, n)
	_, err = io.ReadFull(r, buf)
	if err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return 0, nil, err
	}
	return buf[0], buf[1:], nil
}

// newPacket starts a packet: room for the length, the type and (when withID) the request id.
func newPacket(typ byte, id uint32) []byte {
	b := make([]byte, 4, 64)
	b = append(b, typ)
	return appendU32(b, id)
}

// finish fills in the length of a packet started by newPacket.
func finish(b []byte) []byte {
	binary.BigEndian.PutUint32(b, uint32(len(b)-4))
	return b
}

func appendU32(b []byte, v uint32) []byte {
	return binary.BigEndian.AppendUint32(b, v)
}

func appendU64(b []byte, v uint64) []byte {
	return binary.BigEndian.AppendUint64(b, v)
}

func appendString(b []byte, s string) []byte {
	b = appendU32(b, uint32(len(s)))
	return append(b, s...)
}

// decoder reads big-endian values; the first failure sticks in err.
type decoder struct {
	b   []byte
	err error
}

func (d *decoder) take(n int) []byte {
	if d.err != nil {
		return nil
	}
	if n < 0 || n > len(d.b) {
		d.err = errShort
		return nil
	}
	p := d.b[:n]
	d.b = d.b[n:]
	return p
}

func (d *decoder) u32() uint32 {
	p := d.take(4)
	if p == nil {
		return 0
	}
	return binary.BigEndian.Uint32(p)
}

func (d *decoder) u64() uint64 {
	p := d.take(8)
	if p == nil {
		return 0
	}
	return binary.BigEndian.Uint64(p)
}

// bytes reads a string; the result aliases the packet buffer.
func (d *decoder) bytes() []byte {
	n := d.u32()
	if d.err != nil {
		return nil
	}
	if uint64(n) > uint64(len(d.b)) {
		d.err = errShort
		return nil
	}
	return d.take(int(n))
}

func (d *decoder) str() string {
	return string(d.bytes())
}
