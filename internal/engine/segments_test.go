package engine

import (
	"math"
	"testing"
)

func TestPlanSegmentsHandlesMaxInt64(t *testing.T) {
	s := PlanSegments(math.MaxInt64, 1<<20, 4)
	if len(s) != 4 {
		t.Fatalf("got %d segments, want 4", len(s))
	}
}
