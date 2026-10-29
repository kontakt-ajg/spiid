package sftp

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"testing"
	"time"

	"spiid/internal/errs"
)

func TestPacketTooLarge(t *testing.T) {
	// Case 1: during the handshake.
	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	go func() {
		readPacket(c2sR)
		s2cW.Write([]byte{4, 0, 0, 0}) // 64 MiB, and nothing after it
	}()
	done := make(chan error, 1)
	go func() {
		_, err := NewClient(s2cR, c2sW)
		done <- err
	}()
	select {
	case err := <-done:
		if errs.KindOf(err) != errs.Protocol {
			t.Fatalf("handshake: got %v, want Protocol", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("NewClient did not return: the 64 MiB packet is being read")
	}

	// Case 2: after a good handshake, in answer to a request. Only the 4 length bytes are ever written, so a
	// client that tried to read the body would block here instead of returning.
	for _, length := range [][]byte{{4, 0, 0, 0}, {0, 0, 0, 0}} {
		r, w := fakeServer(t, func(r io.Reader, w io.Writer) {
			readPacket(r)
			w.Write(length)
			time.Sleep(5 * time.Second)
		})
		c, err := NewClient(r, w)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, err = c.Stat(ctx, "/x")
		cancel()
		if errs.KindOf(err) != errs.ConnectionLost || !hasKind(err, errs.Protocol) {
			t.Fatalf("length %v: got %v, want ConnectionLost caused by Protocol", length, err)
		}
		select {
		case <-c.Done():
		default:
			t.Fatal("client not broken")
		}
		_, err = c.Stat(context.Background(), "/y")
		if errs.KindOf(err) != errs.ConnectionLost {
			t.Fatalf("later request: %v", err)
		}
		c.Close()
	}
}

func TestReadPacketLimit(t *testing.T) {
	max := make([]byte, 4, 4+maxPacketSize)
	binary.BigEndian.PutUint32(max, maxPacketSize)
	max = append(max, fxpData)
	max = append(max, make([]byte, maxPacketSize-1)...)
	typ, body, err := readPacket(bytes.NewReader(max))
	if err != nil || typ != fxpData || len(body) != maxPacketSize-1 {
		t.Fatalf("max-size packet: %d %d %v", typ, len(body), err)
	}
	over := []byte{0, 4, 4, 1}
	_, _, err = readPacket(bytes.NewReader(over))
	if errs.KindOf(err) != errs.Protocol {
		t.Fatalf("over limit: %v", err)
	}
}
