package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"spiid/internal/config"
	"spiid/internal/engine"
	"spiid/internal/errs"
	"spiid/internal/plan"
)

// Job is a snapshot of one download.
type Job = engine.Job

// Summary is the totals shown in the status bar.
type Summary struct {
	// Speed is the combined download speed in bytes per second.
	Speed float64
	// Active and Queued count running and waiting jobs.
	Active, Queued int
}

// Download returns at once; in the background it walks paths, queues every file and reports what it skipped.
func (s *Service) Download(server string, paths []string, overwrite bool) error {
	srv, ok := s.findServer(server)
	if !ok {
		return errs.New(errs.InvalidInput, "download", server, errors.New("unknown server"))
	}
	if len(paths) == 0 {
		return errs.New(errs.InvalidInput, "download", server, errors.New("nothing selected"))
	}
	paths = slices.Clone(paths)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errs.New(errs.Canceled, "download", server, nil)
	}
	s.walks.Add(1)
	st := s.settings
	s.mu.Unlock()
	label := paths[0]
	if n := len(paths) - 1; n > 0 {
		label += fmt.Sprintf(" (and %d more)", n)
	}
	ctx, cancel := context.WithCancel(s.ctx)
	scan := &runningScan{Scan: Scan{ID: s.nextScanID(), Server: server, Folder: label}, cancel: cancel}
	s.mu.Lock()
	s.scans = append(s.scans, scan)
	s.mu.Unlock()
	// One scan per selection: the walker also skips duplicates across the selected paths.
	go func() {
		defer s.walks.Done()
		defer cancel()
		err := s.expand(ctx, srv, paths, st.DownloadDir, st.MaxConnections, func(it plan.Item) {
			_, duplicate := s.eng.Add(engine.Request{Server: srv, Remote: it.Remote, Local: it.Local,
				Overwrite: overwrite, Root: st.DownloadDir})
			if duplicate {
				s.addMessage("Already in the queue: " + it.Remote)
				return
			}
			s.mu.Lock()
			scan.Files++
			s.mu.Unlock()
		})
		s.mu.Lock()
		n := scan.Files
		s.scans = slices.DeleteFunc(s.scans, func(v *runningScan) bool { return v.ID == scan.ID })
		s.mu.Unlock()
		switch {
		case err == nil:
			s.recordMessage(fmt.Sprintf("Scanned %s: %d files queued", label, n), false)
		case errs.KindOf(err) == errs.Canceled:
			s.recordMessage(fmt.Sprintf("Scan of %s canceled: %d files queued", label, n), false)
		default:
			s.recordMessage(fmt.Sprintf("Scan of %s stopped: %v; %d files queued", label, err, n), true)
		}
	}()
	return nil
}

func (s *Service) expand(ctx context.Context,
	srv config.Server, paths []string, dir string, maxConns int, emit func(plan.Item),
) error {
	pool := s.ssh.Pool(srv, maxConns)
	conn, err := pool.Get(ctx)
	if err != nil {
		return err
	}
	abortDone := make(chan struct{})
	stopAbort := context.AfterFunc(ctx, func() { pool.Discard(conn); close(abortDone) })
	err = plan.ExpandEach(ctx, conn.SFTP, paths, filepath.Clean(dir), emit, func(k plan.Skipped) {
		s.addMessage(fmt.Sprintf("Skipped %s: %s", k.Remote, k.Reason))
	})
	if !stopAbort() {
		<-abortDone
		return err
	}
	if err != nil {
		pool.Discard(conn)
	} else {
		pool.Put(conn)
	}
	return err
}

// ClearFinished removes done, skipped and canceled downloads from the list (never failed ones).
func (s *Service) ClearFinished() { s.eng.ClearFinished() }

// Jobs returns the downloads, newest first.
func (s *Service) Jobs() []Job { return s.eng.Jobs() }

// Summary adds up the running and queued jobs.
func (s *Service) Summary() Summary { return Summarize(s.eng.Jobs()) }

// Summarize adds up the running and queued jobs of one snapshot.
func Summarize(jobs []Job) Summary {
	var sum Summary
	for _, j := range jobs {
		switch j.State {
		case engine.Running:
			sum.Active++
			sum.Speed += j.Speed
		case engine.Queued:
			sum.Queued++
		}
	}
	return sum
}

// Cancel stops a job.
func (s *Service) Cancel(id int64) error { return s.eng.Cancel(id) }

// Retry requeues a failed or canceled job (and forgets the session secrets of its server).
func (s *Service) Retry(id int64) error {
	err := s.eng.Retry(id)
	if err == nil {
		s.log.Info("download: retrying job %d", id)
	}
	return err
}

// Overwrite requeues a job that failed because the local file exists, with overwrite on.
func (s *Service) Overwrite(id int64) error { return s.eng.Overwrite(id) }
