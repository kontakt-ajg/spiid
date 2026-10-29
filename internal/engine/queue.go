package engine

import (
	"errors"

	"spiid/internal/errs"
)

var errNotActive = errors.New("job is not active")

// Cancel cancels a queued or running job.
func (m *Manager) Cancel(id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, err := m.find(id)
	if err != nil {
		return err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	switch j.pub.State {
	case Queued:
		j.pub.State, j.pub.Err, j.pub.ErrKind = Canceled, "canceled", errs.Canceled
		m.opts.Log.Info("download: canceled %s", j.pub.Remote)
	case Running:
		j.cancel()
	default:
		return errs.New(errs.InvalidInput, "cancel", "", errNotActive)
	}
	return nil
}

// requeue moves a job to Queued if ok(job) holds; m.mu is held.
func (m *Manager) requeue(id int64, ok func(*job) bool, op, why string, overwrite bool) (*job, error) {
	j, err := m.find(id)
	if err != nil {
		return nil, err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if !ok(j) {
		return nil, errs.New(errs.InvalidInput, op, "", errors.New(why))
	}
	if overwrite {
		j.req.Overwrite = true
	}
	j.pub.State, j.pub.Err, j.pub.ErrKind = Queued, "", errs.Other
	return j, nil
}

func retryable(j *job) bool { return j.pub.State == Failed || j.pub.State == Canceled }

// Retry requeues a failed or canceled job.
func (m *Manager) Retry(id int64) error {
	m.mu.Lock()
	j, err := m.find(id)
	if err == nil {
		j.mu.Lock()
		if !retryable(j) {
			err = errs.New(errs.InvalidInput, "retry", "", errors.New("job is not failed or canceled"))
		}
		j.mu.Unlock()
	}
	server := ""
	if err == nil {
		server = j.pub.Server
	}
	m.mu.Unlock()
	if err != nil {
		return err
	}
	if m.opts.OnRetry != nil {
		m.opts.OnRetry(server)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.requeue(id, retryable, "retry", "job is not failed or canceled", false); err != nil {
		return err
	}
	m.schedule()
	return nil
}

// Overwrite requeues a job that failed with LocalExists, with overwrite on.
func (m *Manager) Overwrite(id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err := m.requeue(id, func(j *job) bool {
		return j.pub.State == Failed && j.pub.ErrKind == errs.LocalExists
	}, "overwrite", "job did not fail because a local file exists", true)
	if err != nil {
		return err
	}
	m.schedule()
	return nil
}
