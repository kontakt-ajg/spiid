package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"spiid/internal/errs"
)

func (j *job) source() source {
	s := j.req.Server
	return source{Host: s.Host, Port: s.Port, User: s.User}
}

// checkLinks fails if any of paths, or any folder between Request.Root (excluded) and it, is a symbolic link.
func (j *job) checkLinks(paths ...string) error {
	root := ""
	if j.req.Root != "" {
		root = filepath.Clean(j.req.Root)
	}
	for _, p := range paths {
		p = filepath.Clean(p)
		if isLink(p) {
			return linkError(p)
		}
		if root == "" {
			continue
		}
		rel, err := filepath.Rel(root, p)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		for d := filepath.Dir(p); d != root && d != filepath.Dir(d); d = filepath.Dir(d) {
			if isLink(d) {
				return linkError(d)
			}
		}
	}
	return nil
}

func isLink(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}

func linkError(p string) error {
	return errs.New(errs.LocalWrite, "download", p, errors.New("refusing to write through a symbolic link"))
}

// readOwnState parses the state file; ok only if it parses.
func (j *job) readOwnState() (stateFile, bool) {
	data, err := os.ReadFile(j.statePath())
	if err != nil {
		return stateFile{}, false
	}
	st, err := decodeState(data)
	return st, err == nil
}

// ownsSidecars reports whether the state file parses and names this remote path and size: the part file next
// to it is then a leftover of an older Spiid run and may be replaced.
func (j *job) ownsSidecars() bool {
	st, ok := j.readOwnState()
	return ok && st.RemotePath == j.req.Remote && st.Size == j.size
}

// removeOwnSidecars removes a leftover part and state file, only when the state file is valid for this request.
func (j *job) removeOwnSidecars() {
	st, ok := j.readOwnState()
	if !ok || !st.validFor(j.req.Remote, j.size, j.mtime, j.source(), j.size) {
		return
	}
	os.Remove(j.partPath())
	os.Remove(j.statePath())
}
