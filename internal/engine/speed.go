package engine

import (
	"context"
	"time"
)

const speedAlpha = 0.3

// ewma is the smoothed speed in bytes/s.
type ewma struct {
	speed float64
	have  bool
}

// add feeds one sample: the first sets the speed, later ones are blended 0.3 new + 0.7 old.
func (e *ewma) add(sample float64) float64 {
	if !e.have {
		e.speed, e.have = sample, true
	} else {
		e.speed = speedAlpha*sample + (1-speedAlpha)*e.speed
	}
	return e.speed
}

// sampleSpeed updates the job's Speed every interval until ctx ends. The baseline is the bytes done now, so a
// resumed job does not report a spike.
func (j *job) sampleSpeed(ctx context.Context, interval time.Duration) {
	j.mu.Lock()
	last := j.pub.Done
	j.mu.Unlock()
	lastT := time.Now()
	var e ewma
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			el := now.Sub(lastT).Seconds()
			if el <= 0 {
				continue
			}
			j.mu.Lock()
			bytes := j.pub.Done
			j.pub.Speed = e.add(float64(bytes-last) / el)
			j.mu.Unlock()
			last, lastT = bytes, now
		}
	}
}
