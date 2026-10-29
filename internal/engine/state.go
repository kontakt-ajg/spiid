package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"spiid/internal/fsutil"
)

// stateFile is the content of <local>.spiid-state.
type stateFile struct {
	RemotePath string    `json:"remote_path"`
	Size       int64     `json:"size"`
	MTime      int64     `json:"mtime"`
	Source     source    `json:"source"`
	Segments   []Segment `json:"segments"`
}

// source identifies the server a download comes from; a part file is only resumed from the same source.
type source struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	User string `json:"user"`
}

// rawState has pointers so a missing field is an error.
type rawState struct {
	RemotePath *string `json:"remote_path"`
	Size       *int64  `json:"size"`
	MTime      *int64  `json:"mtime"`
	Source     *struct {
		Host *string `json:"host"`
		Port *int    `json:"port"`
		User *string `json:"user"`
	} `json:"source"`
	Segments *[]struct {
		Start *int64 `json:"start"`
		End   *int64 `json:"end"`
		Done  *int64 `json:"done"`
	} `json:"segments"`
}

// decodeState parses a state file; missing fields and trailing bytes are errors.
func decodeState(data []byte) (stateFile, error) {
	var raw rawState
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&raw); err != nil {
		return stateFile{}, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return stateFile{}, errors.New("trailing data")
	}
	if raw.RemotePath == nil || raw.Size == nil || raw.MTime == nil || raw.Segments == nil || raw.Source == nil ||
		raw.Source.Host == nil || raw.Source.Port == nil || raw.Source.User == nil {
		return stateFile{}, errors.New("missing field")
	}
	st := stateFile{RemotePath: *raw.RemotePath, Size: *raw.Size, MTime: *raw.MTime,
		Source: source{*raw.Source.Host, *raw.Source.Port, *raw.Source.User}}
	for _, s := range *raw.Segments {
		if s.Start == nil || s.End == nil || s.Done == nil {
			return stateFile{}, errors.New("missing segment field")
		}
		st.Segments = append(st.Segments, Segment{Start: *s.Start, End: *s.End, Done: *s.Done})
	}
	return st, nil
}

// validFor reports whether st may be resumed for the given remote values and an existing part file of partSize.
func (st stateFile) validFor(remote string, size int64, mtime time.Time, src source, partSize int64) bool {
	if st.Source != src || st.RemotePath != remote || st.Size != size || st.MTime != mtime.Unix() || partSize != size {
		return false
	}
	if len(st.Segments) == 0 {
		return false
	}
	next := int64(0)
	for _, s := range st.Segments {
		if s.Start != next || s.End <= s.Start || s.Done < 0 || s.Done > s.End-s.Start {
			return false
		}
		next = s.End
	}
	return next == size
}

// loadState returns the segments of a usable state file, or false to start fresh.
func loadState(statePath, partPath, remote string, size int64, mtime time.Time, src source) ([]Segment, bool) {
	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, false
	}
	st, err := decodeState(data)
	if err != nil {
		return nil, false
	}
	fi, err := os.Stat(partPath)
	if err != nil || !fi.Mode().IsRegular() {
		return nil, false
	}
	if !st.validFor(remote, size, mtime, src, fi.Size()) {
		return nil, false
	}
	return st.Segments, true
}

// saveState snapshots the segments under the job lock, syncs the part file, then writes the state file, so the
// data is on disk before the state that claims it.
func (j *job) saveState() error {
	j.saveMu.Lock()
	defer j.saveMu.Unlock()
	j.mu.Lock()
	segs := append([]Segment(nil), j.segs...)
	part := j.part
	j.mu.Unlock()
	if part != nil {
		if err := part.Sync(); err != nil {
			return err
		}
	}
	data, err := json.Marshal(stateFile{
		RemotePath: j.req.Remote,
		Size:       j.size,
		MTime:      j.mtime.Unix(),
		Source:     j.source(),
		Segments:   segs,
	})
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(j.statePath(), data, 0o600)
}
