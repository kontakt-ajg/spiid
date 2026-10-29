package plan

import (
	"context"
	"strings"
	"testing"

	"spiid/internal/sftp"
)

func TestWalkerLimits(t *testing.T) {
	for _, tc := range []struct {
		depth, count int
		want         string
	}{
		{256, 0, "depth limit 256"}, {0, 1_000_000, "item limit 1000000"},
	} {
		w := walker{ctx: context.Background(), depth: tc.depth, count: tc.count}
		err := w.visit("/x", "/", sftp.FileInfo{})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v", err)
		}
	}
}

func TestDeepTreeStops(t *testing.T) {
	e := newEnv(t, false)
	e.file(t, strings.Repeat("/d", 257)+"/file")
	_, _, err := Expand(context.Background(), e.c, []string{"/d"}, e.dl)
	if err == nil || !strings.Contains(err.Error(), "depth limit 256") {
		t.Fatalf("%v", err)
	}
}
