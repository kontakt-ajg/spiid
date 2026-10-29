package engine

import (
	"errors"
	"io"
	"testing"
	"time"

	"spiid/internal/errs"
)

func TestPlanSegments(t *testing.T) {
	const mib = 1 << 20
	if PlanSegments(0, mib, 4) != nil || PlanSegments(-5, mib, 4) != nil {
		t.Error("size <= 0 must give nil")
	}
	cases := []struct {
		size, min int64
		threads   int
		want      int
	}{
		{1, mib, 4, 1}, {mib, mib, 4, 1}, {mib + 1, mib, 4, 2}, {10 * mib, mib, 4, 4},
		{10 * mib, mib, 1, 1}, {10 * mib, mib, 0, 1}, {10 * mib, mib, -3, 1}, {3 * mib, mib, 10, 3},
	}
	for _, c := range cases {
		segs := PlanSegments(c.size, c.min, c.threads)
		if len(segs) != c.want {
			t.Errorf("PlanSegments(%d,%d,%d) = %d segments, want %d", c.size, c.min, c.threads, len(segs), c.want)
			continue
		}
		next := int64(0)
		for _, s := range segs {
			if s.Start != next || s.End <= s.Start || s.Done != 0 {
				t.Errorf("bad segment %+v (want start %d)", s, next)
			}
			next = s.End
		}
		if next != c.size {
			t.Errorf("last segment ends at %d, want %d", next, c.size)
		}
	}
}

func TestBackoff(t *testing.T) {
	b := backoff{waits: []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}}
	for _, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
		w, ok := b.next(false)
		if !ok || w != want {
			t.Fatalf("next = %v %v, want %v true", w, ok, want)
		}
	}
	if _, ok := b.next(false); ok {
		t.Fatal("fourth failure must be final")
	}
	if w, ok := b.next(true); !ok || w != time.Second {
		t.Fatalf("after progress: %v %v, want 1s true", w, ok)
	}
	b.next(false)
	b.next(false)
	if _, ok := b.next(false); ok {
		t.Fatal("count must run out again after the reset")
	}
	var none backoff
	if _, ok := none.next(false); ok {
		t.Fatal("no waits: first failure is final")
	}
}

func TestClassify(t *testing.T) {
	permanent := []errs.Kind{errs.NotFound, errs.PermissionDenied, errs.HostKeyMismatch, errs.HostKeyRejected,
		errs.AuthFailed, errs.PoolClosed, errs.LocalWrite, errs.RemoteChanged, errs.LocalExists, errs.Canceled,
		errs.InvalidInput, errs.Locked, errs.WrongMasterPassword}
	transient := []errs.Kind{errs.ConnectionLost, errs.Protocol, errs.Other}
	for _, k := range permanent {
		if !isPermanent(errs.New(k, "op", "p", nil)) {
			t.Errorf("%v must be permanent", k)
		}
	}
	for _, k := range transient {
		if isPermanent(errs.New(k, "op", "p", nil)) {
			t.Errorf("%v must be transient", k)
		}
	}
	if isPermanent(errs.New(errs.ConnectionLost, "read", "p", io.EOF)) {
		t.Error("ConnectionLost wrapping io.EOF must be transient")
	}
	if isPermanent(io.EOF) {
		t.Error("a plain io.EOF must be transient")
	}
	if isPermanent(errors.New("boom")) {
		t.Error("a plain error must be transient")
	}
}

func TestSpeedEWMA(t *testing.T) {
	var e ewma
	samples := []float64{100, 200, 0, 1000}
	want := []float64{100, 0.3*200 + 0.7*100, 0.3*0 + 0.7*(0.3*200+0.7*100), 0}
	want[3] = 0.3*1000 + 0.7*want[2]
	for i, s := range samples {
		if got := e.add(s); got != want[i] {
			t.Errorf("sample %d: speed = %v, want %v", i, got, want[i])
		}
	}
}
