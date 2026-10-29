package engine

import (
	"context"
	"sync"
	"time"

	"spiid/internal/sshconn"
)

// runWorkers runs one worker per unfinished segment, plus the speed sampler and the periodic state saver. The
// first permanent error cancels the others and is returned.
func (j *job) runWorkers(ctx context.Context) error {
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var bg sync.WaitGroup
	bg.Add(2)
	go func() { defer bg.Done(); j.sampleSpeed(wctx, j.cfg.speedEvery) }()
	go func() { defer bg.Done(); j.saveLoop(wctx) }()

	var wg sync.WaitGroup
	var once sync.Once
	var first error
	j.mu.Lock()
	todo := make([]int, 0, len(j.segs))
	for i, s := range j.segs {
		if s.Done < s.End-s.Start {
			todo = append(todo, i)
		}
	}
	j.mu.Unlock()
	for _, i := range todo {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := j.runSegment(wctx, i); err != nil {
				once.Do(func() { first = err; cancel() })
			}
		}()
	}
	wg.Wait()
	cancel()
	bg.Wait()
	return first
}

// saveLoop saves the state every SaveInterval until ctx ends.
func (j *job) saveLoop(ctx context.Context) {
	t := time.NewTicker(j.cfg.saveEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			j.saveState()
		}
	}
}

func (j *job) segment(i int) Segment {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.segs[i]
}

// progress adds n bytes to segment i and to the job.
func (j *job) progress(i, n int) {
	j.mu.Lock()
	j.segs[i].Done += int64(n)
	j.pub.Done += int64(n)
	j.mu.Unlock()
}

func (j *job) addActive(d int) {
	j.mu.Lock()
	j.pub.ActiveSegments += d
	j.mu.Unlock()
}

// runSegment downloads segment i, retrying transient errors.
func (j *job) runSegment(ctx context.Context, i int) error {
	return retry(ctx, j.cfg.waits, func() (bool, error) {
		before := j.segment(i).Done
		err := j.attempt(ctx, i)
		return j.segment(i).Done > before, err
	})
}

// attempt is one try: take a connection, check the remote file, read the rest of the segment. A cancel discards
// the connection (which interrupts a blocked read); otherwise it is returned exactly once.
func (j *job) attempt(ctx context.Context, i int) error {
	seg := j.segment(i)
	if seg.Done >= seg.End-seg.Start {
		return nil
	}
	pool := j.cfg.pool
	conn, err := pool.Get(ctx)
	if err != nil {
		return err
	}
	j.addActive(1)
	defer j.addActive(-1)
	stop := context.AfterFunc(ctx, func() { pool.Discard(conn) })
	err = j.readSegment(ctx, conn, i, seg)
	if !stop() {
		return err // the cancel already discarded conn
	}
	release(pool, conn, err)
	return err
}

func (j *job) readSegment(ctx context.Context, conn *sshconn.Conn, i int, seg Segment) error {
	info, err := conn.SFTP.Stat(ctx, j.req.Remote)
	if err != nil {
		return err
	}
	if err := j.checkInfo(info); err != nil {
		return err
	}
	f, err := conn.SFTP.Open(ctx, j.req.Remote)
	if err != nil {
		return err
	}
	err = f.ReadRange(ctx, seg.Start+seg.Done, seg.End, j.part, func(n int) { j.progress(i, n) })
	f.Close()
	return err
}
