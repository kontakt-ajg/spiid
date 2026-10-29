package engine

import (
	"errors"
	"os"
	"time"

	"spiid/internal/errs"
)

var linkFile = os.Link

const (
	partSuffix  = ".spiid-part"
	stateSuffix = ".spiid-state"
)

func (j *job) partPath() string { return j.req.Local + partSuffix }

func (j *job) statePath() string { return j.req.Local + stateSuffix }

// openPart opens the part file read-write: an existing one for resume, or a new one of size bytes (created
// exclusively, so a file Spiid did not create is never adopted).
func openPart(path string, size int64, resume bool) (*os.File, error) {
	if resume {
		f, err := os.OpenFile(path, os.O_RDWR, 0o600)
		if err != nil {
			return nil, errs.New(errs.LocalWrite, "open", path, err)
		}
		return chmodPart(f, path)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, errs.New(errs.LocalWrite, "create", path, err)
	}
	if _, err := chmodPart(f, path); err != nil {
		return nil, err
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		return nil, errs.New(errs.LocalWrite, "size", path, err)
	}
	return f, nil
}

// finishPart syncs and closes the part file, sets mode and mtime and moves it to local. Without overwrite it
// links and removes the part, so a file that appeared meanwhile is never replaced (LocalExists, part kept).
func finishPart(f *os.File, part, local string, mtime time.Time, overwrite bool) error {
	if err := f.Sync(); err != nil {
		f.Close()
		return errs.New(errs.LocalWrite, "sync", part, err)
	}
	if err := f.Close(); err != nil {
		return errs.New(errs.LocalWrite, "close", part, err)
	}
	if err := os.Chmod(part, finishedMode); err != nil {
		return errs.New(errs.LocalWrite, "chmod", part, err)
	}
	if err := os.Chtimes(part, mtime, mtime); err != nil {
		return errs.New(errs.LocalWrite, "set time", part, err)
	}
	if overwrite {
		if err := os.Rename(part, local); err != nil {
			return errs.New(errs.LocalWrite, "rename", local, err)
		}
		return nil
	}
	if err := linkFile(part, local); err != nil {
		if errors.Is(err, os.ErrExist) {
			return existsErr(local, "exists and differs")
		}
		if _, inspectErr := os.Lstat(local); inspectErr == nil {
			return existsErr(local, "exists and differs")
		} else if !os.IsNotExist(inspectErr) {
			return errs.New(errs.LocalWrite, "inspect", local, inspectErr)
		}
		if err := os.Rename(part, local); err != nil {
			return errs.New(errs.LocalWrite, "rename", local, err)
		}
		return nil
	}
	os.Remove(part)
	return nil
}

func chmodPart(f *os.File, path string) (*os.File, error) {
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return nil, errs.New(errs.LocalWrite, "chmod", path, err)
	}
	return f, nil
}
