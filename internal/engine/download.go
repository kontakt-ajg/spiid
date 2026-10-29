package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"spiid/internal/errs"
	"spiid/internal/logx"
	"spiid/internal/sftp"
	"spiid/internal/sshconn"
)

// runCfg is the configuration a job reads when it starts.
type runCfg struct {
	log        *logx.Logger
	pool       Pool
	threads    int
	minSegment int64
	waits      []time.Duration
	saveEvery  time.Duration
	speedEvery time.Duration
}

// job is one download. mu guards pub, segs, part and req.Overwrite.
type job struct {
	started time.Time
	cfg     runCfg
	req     Request
	cancel  context.CancelFunc

	mu      sync.Mutex
	pub     Job
	segs    []Segment
	part    *os.File
	skipped bool

	saveMu sync.Mutex
	size   int64 // set once, before any worker starts
	mtime  time.Time
}

func existsErr(path, what string) error {
	return errs.Message(errs.LocalExists, fmt.Sprintf("local file %s %s", path, what))
}

// release returns conn to the pool: Put after success, Discard after any error.
func release(p Pool, conn *sshconn.Conn, err error) {
	if err == nil {
		p.Put(conn)
	} else {
		p.Discard(conn)
	}
}

// statRemote reads the remote file's info with retries.
func (j *job) statRemote(ctx context.Context) (sftp.FileInfo, error) {
	var info sftp.FileInfo
	err := retry(ctx, j.cfg.waits, func() (bool, error) {
		conn, err := j.cfg.pool.Get(ctx)
		if err != nil {
			return false, err
		}
		info, err = conn.SFTP.Stat(ctx, j.req.Remote)
		release(j.cfg.pool, conn, err)
		return false, err
	})
	return info, err
}

// checkInfo returns RemoteChanged if info differs from what the job started with.
func (j *job) checkInfo(info sftp.FileInfo) error {
	if info.Size != j.size || info.ModTime.Unix() != j.mtime.Unix() {
		return errs.New(errs.RemoteChanged, "", "", nil)
	}
	return nil
}

// run checks the source and local files, then downloads or skips the file.
func (j *job) run(ctx context.Context) error {
	info, err := j.statRemote(ctx)
	if err != nil {
		return err
	}
	if !info.IsRegular() {
		return errs.New(errs.InvalidInput, "download", j.req.Remote, errors.New("not a regular file"))
	}
	j.size, j.mtime = info.Size, info.ModTime
	j.mu.Lock()
	j.pub.Size = j.size
	overwrite := j.req.Overwrite
	j.mu.Unlock()
	local := j.req.Local
	if err := j.checkLinks(local, j.partPath(), j.statePath()); err != nil {
		return err
	}
	if fi, err := os.Stat(local); err == nil && !overwrite {
		if fi.Mode().IsRegular() && fi.Size() == j.size && fi.ModTime().Unix() == j.mtime.Unix() {
			j.removeOwnSidecars()
			j.mu.Lock()
			j.skipped, j.pub.Done = true, j.size
			j.mu.Unlock()
			return nil
		}
		return existsErr(local, "exists and differs")
	}
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		return errs.New(errs.LocalWrite, "create folder", filepath.Dir(local), err)
	}
	if j.size == 0 {
		if err := j.finishEmpty(); err != nil {
			return err
		}
		j.cfg.log.Info("download: started %s (%d segments)", j.req.Remote, 0)
		return nil
	}
	return j.transfer(ctx, overwrite)
}

// transfer resumes or creates a partial file, runs workers, verifies and publishes it.
func (j *job) transfer(ctx context.Context, overwrite bool) error {
	segs, resume := loadState(j.statePath(), j.partPath(), j.req.Remote, j.size, j.mtime, j.source())
	if !resume {
		if _, err := os.Lstat(j.statePath()); err == nil {
			if !j.ownsSidecars() {
				return foreignSidecar(j.statePath())
			}
		} else if !os.IsNotExist(err) {
			return errs.New(errs.LocalWrite, "inspect", j.statePath(), err)
		}
		if _, err := os.Lstat(j.partPath()); err == nil {
			if !j.ownsSidecars() {
				return foreignSidecar(j.partPath())
			}
			os.Remove(j.partPath())
			os.Remove(j.statePath())
		}
		segs = PlanSegments(j.size, j.cfg.minSegment, j.cfg.threads)
	}
	part, err := openPart(j.partPath(), j.size, resume)
	if err != nil {
		return err
	}
	var done int64
	for _, s := range segs {
		done += s.Done
	}
	j.mu.Lock()
	j.segs, j.part = segs, part
	j.pub.Segments, j.pub.Done = len(segs), done
	j.cfg.log.Info("download: started %s (%d segments)", j.req.Remote, len(segs))
	j.mu.Unlock()
	if err := j.saveState(); err != nil {
		part.Close()
		return errs.New(errs.LocalWrite, "save state", j.statePath(), err)
	}
	err = j.runWorkers(ctx)
	if err == nil {
		err = j.finalCheck(ctx)
	}
	if err == nil {
		err = j.checkLinks(j.req.Local, j.partPath())
	}
	if err == nil {
		err = finishPart(part, j.partPath(), j.req.Local, j.mtime, overwrite)
		j.mu.Lock()
		j.part = nil
		j.mu.Unlock()
		if err == nil {
			os.Remove(j.statePath())
			return nil
		}
	}
	if errs.KindOf(err) == errs.RemoteChanged {
		part.Close()
		os.Remove(j.partPath())
		os.Remove(j.statePath())
		return err
	}
	j.saveState()
	part.Close()
	return err
}

// finalCheck verifies that the remote file still has the size and mtime the job started with.
func (j *job) finalCheck(ctx context.Context) error {
	info, err := j.statRemote(ctx)
	if err != nil {
		return err
	}
	return j.checkInfo(info)
}
