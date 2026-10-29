// Package fsutil has small file helpers: atomic writes and "~" expansion.
package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// WriteFileAtomic writes data to a temp file in path's folder, syncs it, sets perm, closes it and renames it
// over path. The folder must exist.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir, base := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	f, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	err = writeAndClose(f, data, perm)
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func writeAndClose(f *os.File, data []byte, perm os.FileMode) error {
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ExpandHome replaces a leading "~" or "~/" (also "~\" on Windows) with home. Anything else is unchanged.
func ExpandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	if runtime.GOOS == "windows" && strings.HasPrefix(path, `~\`) {
		return filepath.Join(home, path[2:])
	}
	return path
}
