package engine

import (
	"context"
	"time"

	"spiid/internal/errs"
)

// isPermanent reports whether retrying err cannot help. It classifies by kind only: a connection error that
// wraps io.EOF is still transient, and a plain error is Other (transient).
func isPermanent(err error) bool {
	return errs.KindOf(err).Permanent()
}

// backoff hands out the waits between attempts. k counts consecutive failed attempts.
type backoff struct {
	waits []time.Duration
	k     int
}

// next is called after a failed attempt; progress says the attempt made progress (which resets the count).
// ok is false when the failure is final.
func (b *backoff) next(progress bool) (wait time.Duration, ok bool) {
	if progress {
		b.k = 0
	}
	if b.k >= len(b.waits) {
		return 0, false
	}
	wait = b.waits[b.k]
	b.k++
	return wait, true
}

// sleep waits d or until ctx ends (then it returns a Canceled error).
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return errs.New(errs.Canceled, "wait", "", ctx.Err())
	}
}

// retry runs fn until it succeeds, fails permanently, runs out of waits or ctx ends. fn reports whether the
// attempt made progress.
func retry(ctx context.Context, waits []time.Duration, fn func() (progress bool, err error)) error {
	b := backoff{waits: waits}
	for {
		progress, err := fn()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return errs.New(errs.Canceled, "download", "", ctx.Err())
		}
		if isPermanent(err) {
			return err
		}
		wait, ok := b.next(progress)
		if !ok {
			return err
		}
		if err := sleep(ctx, wait); err != nil {
			return err
		}
	}
}
