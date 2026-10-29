package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const sampleSize = 50 << 20

// makeSample creates n files sample-<i>.bin of 50 MiB random data and a tree/ folder with 3 nested levels of 20
// small files each under root. Files that already exist are skipped. All work goes through an os.Root, so a
// symlink inside root can never lead the writes outside it.
func makeSample(root string, n int) error {
	err := os.MkdirAll(root, 0o755)
	if err != nil {
		return err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	for i := 0; i < n; i++ {
		err = writeIfMissing(r, fmt.Sprintf("sample-%d.bin", i), func(w io.Writer) error {
			_, err := io.CopyN(w, rand.Reader, sampleSize)
			return err
		})
		if err != nil {
			return err
		}
	}
	dir := "tree"
	for level := 1; level <= 3; level++ {
		err = mkdirNoLinks(r, dir)
		if err != nil {
			return err
		}
		for i := 0; i < 20; i++ {
			p := filepath.Join(dir, fmt.Sprintf("file-%02d.txt", i))
			err = writeIfMissing(r, p, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "level %d file %d\n", level, i)
				return err
			})
			if err != nil {
				return err
			}
		}
		dir = filepath.Join(dir, fmt.Sprintf("level%d", level+1))
	}
	return nil
}

// mkdirNoLinks creates dir (relative to r) if missing and refuses a dir that exists as a symlink or non-folder.
func mkdirNoLinks(r *os.Root, dir string) error {
	fi, err := r.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return r.MkdirAll(dir, 0o755)
	}
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("sample path %q exists and is not a real folder (symlink or file); remove it first", dir)
	}
	return nil
}

// writeIfMissing creates p (relative to r) through a temporary file that is hard-linked into place, so an
// interrupted run never leaves a short file behind and a destination that appeared meanwhile is never replaced.
func writeIfMissing(r *os.Root, p string, fill func(io.Writer) error) error {
	fi, err := r.Lstat(p)
	if err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("sample path %q is a symlink; remove it first", p)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var f *os.File
	var tmp string
	for {
		var id [16]byte
		_, err = rand.Read(id[:])
		if err != nil {
			return err
		}
		tmp = filepath.Join(filepath.Dir(p), fmt.Sprintf(".sample-%x", id))
		f, err = r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		break
	}
	defer r.Remove(tmp)
	err = fill(f)
	if err != nil {
		f.Close()
		return err
	}
	err = f.Close()
	if err != nil {
		return err
	}
	err = r.Link(tmp, p)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	return err
}
