package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"spiid/internal/errs"
	"spiid/internal/fsutil"
)

// write saves next as config.json (atomically, 0600 in a 0700 folder) and makes it the in-memory state.
func (s *Store) write(next configFile) error {
	if next.Servers == nil {
		next.Servers = []savedServer{}
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, "config.json")
	failed := func(err error) error { return errs.New(errs.LocalWrite, "save config", path, err) }
	if err = os.MkdirAll(s.dir, 0700); err != nil {
		return failed(err)
	}
	if runtime.GOOS != "windows" {
		if err = os.Chmod(s.dir, 0700); err != nil {
			return failed(err)
		}
	}
	_, before := os.Stat(path)
	if err = fsutil.WriteFileAtomic(path, append(data, '\n'), 0600); err != nil {
		return failed(err)
	}
	// The file is replaced now: memory must match it even if syncing the folder fails below.
	changedServers := !slices.Equal(s.file.Servers, next.Servers)
	s.file = next
	if changedServers {
		s.log.Info("config: saved (%d servers)", len(next.Servers))
	}
	if os.IsNotExist(before) {
		s.log.Info("config: config.json created")
	}
	if err = syncDir(s.dir); err != nil {
		return failed(err)
	}
	return nil
}

// syncDir makes a rename inside dir durable (not supported on Windows, where it is not needed).
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	folder, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = folder.Sync()
	if closeErr := folder.Close(); err == nil {
		err = closeErr
	}
	return err
}
