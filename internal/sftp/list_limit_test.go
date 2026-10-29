package sftp

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"spiid/internal/errs"
)

func TestEndlessListing(t *testing.T) {
	for _, n := range []uint32{0, 1024} {
		t.Run(string(rune('a'+n/1024)), func(t *testing.T) {
			var requests atomic.Int32
			c := probeClient(t, func(r io.Reader, w io.Writer) {
				for {
					_, b, err := readPacket(r)
					if err != nil {
						return
					}
					requests.Add(1)
					p := appendU32(newPacket(fxpName, probeID(b)), n)
					for range n {
						p = appendString(p, ".")
						p = appendString(p, "")
						p = appendU32(p, 0)
					}
					if _, err := w.Write(finish(p)); err != nil {
						return
					}
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			entries, err := c.readEntries(ctx, "/", "handle")
			if errs.KindOf(err) != errs.Protocol {
				t.Fatalf("%v", err)
			}
			if len(entries) != 0 {
				t.Fatal("retained ignored entries")
			}
			if n != 0 && (!strings.Contains(err.Error(), "directory listing too large") ||
				requests.Load() != maxDirEntries/int32(n)+1) {
				t.Fatalf("%d requests: %v", requests.Load(), err)
			}
		})
	}
}

func TestListingWithHugeNamesStops(t *testing.T) {
	name := strings.Repeat("n", 32<<10)
	var requests atomic.Int32
	c := probeClient(t, func(r io.Reader, w io.Writer) {
		for {
			_, b, err := readPacket(r)
			if err != nil {
				return
			}
			i := requests.Add(1)
			p := appendU32(newPacket(fxpName, probeID(b)), 4)
			for k := range 4 {
				p = appendString(p, fmt.Sprintf("%s-%d-%d", name, i, k))
				p = appendString(p, "")
				p = appendU32(p, 0)
			}
			if _, err := w.Write(finish(p)); err != nil {
				return
			}
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := c.readEntries(ctx, "/", "handle")
	if errs.KindOf(err) != errs.Protocol || !strings.Contains(err.Error(), "directory listing too large") {
		t.Fatalf("%v", err)
	}
	if got, limit := int(requests.Load()), maxDirNameBytes/(4*len(name))+1; got > limit {
		t.Fatalf("%d requests, want at most %d", got, limit)
	}
}
