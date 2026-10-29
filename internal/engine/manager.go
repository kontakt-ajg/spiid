package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"spiid/internal/errs"
)

// Manager owns the job queue and the scheduler.
type Manager struct {
	opts Options

	mu       sync.Mutex
	jobs     []*job // in id order
	byKey    map[string]*job
	nextID   int64
	running  int
	closed   bool
	threads  int
	parallel int
	maxConns int
	wg       sync.WaitGroup
}

// NewManager creates a Manager; zero Options fields take their defaults.
func NewManager(opts Options) *Manager {
	if opts.MinSegment <= 0 {
		opts.MinSegment = 1 << 20
	}
	if opts.RetryWaits == nil {
		opts.RetryWaits = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	}
	if opts.SaveInterval <= 0 {
		opts.SaveInterval = time.Second
	}
	if opts.SpeedInterval <= 0 {
		opts.SpeedInterval = 500 * time.Millisecond
	}
	return &Manager{
		opts:     opts,
		byKey:    map[string]*job{},
		threads:  max(1, opts.ThreadsPerFile),
		parallel: max(1, opts.ParallelFiles),
		maxConns: max(1, opts.MaxConnections),
	}
}

// Add deduplicates active jobs by local path and restarts completed jobs with the new request.
func (m *Manager) Add(req Request) (Job, bool) {
	key := strings.ToLower(strings.ToUpper(req.Local))
	m.mu.Lock()
	defer m.mu.Unlock()
	if j, ok := m.byKey[key]; ok {
		j.mu.Lock()
		if j.pub.State == Queued || j.pub.State == Running {
			pub := j.pub
			j.mu.Unlock()
			return pub, true
		}
		j.req = req
		j.pub = Job{ID: j.pub.ID, Server: req.Server.Name, Remote: req.Remote,
			Local: req.Local, State: Queued, Added: time.Now()}
		j.mu.Unlock()
		m.opts.Log.Info("download: added %s:%s → %s", req.Server.Name, req.Remote, req.Local)
		m.schedule()
		j.mu.Lock()
		defer j.mu.Unlock()
		return j.pub, false
	}
	m.nextID++
	j := &job{req: req}
	j.pub = Job{
		ID:     m.nextID,
		Server: req.Server.Name,
		Remote: req.Remote,
		Local:  req.Local,
		State:  Queued,
		Added:  time.Now(),
	}
	m.opts.Log.Info("download: added %s:%s → %s", j.pub.Server, j.pub.Remote, j.pub.Local)
	m.jobs = append(m.jobs, j)
	m.byKey[key] = j
	m.schedule()
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.pub, false
}

// schedule starts queued jobs in id order while fewer than ParallelFiles run. m.mu is held.
func (m *Manager) schedule() {
	for _, j := range m.jobs {
		if m.closed || m.running >= m.parallel {
			return
		}
		j.mu.Lock()
		queued := j.pub.State == Queued
		j.mu.Unlock()
		if queued {
			m.start(j)
		}
	}
}

func (m *Manager) start(j *job) {
	ctx, cancel := context.WithCancel(context.Background())
	j.cfg = runCfg{
		log:        m.opts.Log,
		pool:       m.opts.Pools.Pool(j.req.Server, m.maxConns),
		threads:    m.threads,
		minSegment: m.opts.MinSegment,
		waits:      m.opts.RetryWaits,
		saveEvery:  m.opts.SaveInterval,
		speedEvery: m.opts.SpeedInterval,
	}
	j.mu.Lock()
	j.cancel = cancel
	j.started = time.Now()
	j.skipped = false
	j.pub.State, j.pub.Err, j.pub.ErrKind, j.pub.Speed = Running, "", errs.Other, 0
	j.mu.Unlock()
	m.running++
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.finish(j, j.run(ctx))
	}()
}

func (m *Manager) finish(j *job, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j.mu.Lock()
	switch {
	case err == nil && j.skipped:
		j.pub.State = Skipped
		reason := j.pub.Err
		if reason == "" {
			reason = "already downloaded"
		}
		m.opts.Log.Info("download: skipped %s (%s)", j.pub.Remote, reason)
	case err == nil:
		j.pub.State, j.pub.Done = Done, j.pub.Size
		duration := max(time.Since(j.started), time.Millisecond)
		m.opts.Log.Info("download: done %s (%d bytes, %s, %.0f B/s average)", j.pub.Remote, j.pub.Size,
			duration.Round(time.Millisecond), float64(j.pub.Size)/duration.Seconds())
	case errs.KindOf(err) == errs.Canceled:
		j.pub.State, j.pub.Err, j.pub.ErrKind = Canceled, err.Error(), errs.Canceled
		m.opts.Log.Info("download: canceled %s", j.pub.Remote)
	default:
		j.pub.State, j.pub.Err, j.pub.ErrKind = Failed, err.Error(), errs.KindOf(err)
		m.opts.Log.Error("download: failed %s (%s)", j.pub.Remote, j.pub.Err)
	}
	j.pub.Speed, j.pub.ActiveSegments = 0, 0
	j.cancel()
	j.mu.Unlock()
	m.running--
	m.schedule()
}

func (m *Manager) find(id int64) (*job, error) {
	for _, j := range m.jobs {
		if j.pub.ID == id {
			return j, nil
		}
	}
	return nil, errs.New(errs.InvalidInput, "job", "", errors.New("no such job"))
}

// Jobs returns snapshots, newest first.
func (m *Manager) Jobs() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Job, 0, len(m.jobs))
	for i := len(m.jobs) - 1; i >= 0; i-- {
		j := m.jobs[i]
		j.mu.Lock()
		out = append(out, j.pub)
		j.mu.Unlock()
	}
	return out
}

// SetLimits changes the limits for jobs that start afterwards; a larger parallel value starts more queued jobs.
func (m *Manager) SetLimits(threads, parallel, maxConns int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.threads, m.parallel, m.maxConns = max(1, threads), max(1, parallel), max(1, maxConns)
	m.schedule()
}

// Close stops all starts, cancels running jobs and waits for them; state files stay.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	for _, j := range m.jobs {
		j.mu.Lock()
		if j.pub.State == Running {
			j.cancel()
		}
		j.mu.Unlock()
	}
	m.mu.Unlock()
	m.wg.Wait()
}
