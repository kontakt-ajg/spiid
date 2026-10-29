package engine

import (
	"os"
	"spiid/internal/errs"
)

func (j *job) finishEmpty() error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if j.req.Overwrite {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(j.req.Local, flags, finishedMode)
	if err != nil {
		if os.IsExist(err) {
			return existsErr(j.req.Local, "exists and differs")
		}
		return errs.New(errs.LocalWrite, "create", j.req.Local, err)
	}
	if err := f.Chmod(finishedMode); err != nil {
		f.Close()
		return errs.New(errs.LocalWrite, "chmod", j.req.Local, err)
	}
	if err := f.Close(); err != nil {
		return errs.New(errs.LocalWrite, "close", j.req.Local, err)
	}
	if err := os.Chtimes(j.req.Local, j.mtime, j.mtime); err != nil {
		return errs.New(errs.LocalWrite, "set time", j.req.Local, err)
	}
	j.removeOwnSidecars()
	return nil
}

func foreignSidecar(path string) error {
	return errs.Message(errs.LocalWrite, path+" was not created by Spiid; remove it to download this file")
}
