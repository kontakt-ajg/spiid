// Package engine downloads files in segments over pooled SFTP connections, with resume, retries and a job queue.
package engine

// Segment is one byte range of a file.
type Segment struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`  // exclusive
	Done  int64 `json:"done"` // bytes done from Start
}

// PlanSegments splits size bytes into n = clamp(ceil(size/minSegment), 1, threads) contiguous segments; the
// last one ends at exactly size. size <= 0 gives nil.
func PlanSegments(size, minSegment int64, threads int) []Segment {
	if size <= 0 {
		return nil
	}
	if minSegment < 1 {
		minSegment = 1
	}
	if threads < 1 {
		threads = 1
	}
	n := 1 + (size-1)/minSegment
	n = max(1, min(n, int64(threads)))
	chunk := size / n
	segs := make([]Segment, n)
	for i := range segs {
		segs[i].Start = int64(i) * chunk
		segs[i].End = int64(i+1) * chunk
	}
	segs[n-1].End = size
	return segs
}
