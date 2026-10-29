package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"spiid/internal/app"
	"spiid/internal/errs"
)

type wireError struct {
	// Kind is the stable error category.
	Kind string `json:"kind"`
	// Message describes the error.
	Message string `json:"message"`
}

func errorJSON(err error) *wireError {
	if err == nil {
		return nil
	}
	return &wireError{errs.KindOf(err).String(), err.Error()}
}

func marshal(v any) string { data, _ := json.Marshal(v); return string(data) }

// decode reads one strict JSON object and checks the method's required fields.
func decode(data []byte, v any) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return invalid(errors.New("expected object"))
	}
	if err := decodeJSON(bytes.NewReader(data), v); err != nil {
		return invalid(err)
	}
	return checkRequired(v)
}

func decodeJSON(r io.Reader, v any) error {
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		if err != nil {
			return err
		}
		return errors.New("trailing JSON")
	}
	return nil
}

func invalid(err error) error { return errs.New(errs.InvalidInput, "params", "", err) }

type entryJSON struct {
	// Name is the file or folder name.
	Name string `json:"name"`
	// Path is the absolute remote path.
	Path string `json:"path"`
	// IsDir reports whether the entry resolves to a directory.
	IsDir bool `json:"is_dir"`
	// IsLink reports whether the entry is a symbolic link.
	IsLink bool `json:"is_link"`
	// Size is the file size in bytes.
	Size int64 `json:"size"`
	// Mtime is the modification time in seconds since the Unix epoch.
	Mtime int64 `json:"mtime"`
}

func listingJSON(l app.Listing) any {
	entries := make([]entryJSON, 0, len(l.Entries))
	for _, e := range l.Entries {
		entries = append(entries, entryJSON{e.Name, e.Path, e.IsDir, e.IsLink, e.Size, e.ModTime.Unix()})
	}
	return struct {
		// Path is the absolute path.
		Path string `json:"path"`
		// Entries contains the folder entries.
		Entries []entryJSON `json:"entries"`
	}{l.Path, entries}
}

type jobJSON struct {
	// ID identifies the download job.
	ID int64 `json:"id"`
	// Server identifies the configured remote server.
	Server string `json:"server"`
	// Remote is the remote file path.
	Remote string `json:"remote"`
	// Local is the local file path.
	Local string `json:"local"`
	// State is the download state.
	State string `json:"state"`
	// Size is the file size in bytes.
	Size int64 `json:"size"`
	// Done counts downloaded bytes.
	Done int64 `json:"done"`
	// Speed is the download speed in bytes per second.
	Speed float64 `json:"speed"`
	// Segments counts the planned segments.
	Segments int `json:"segments"`
	// ActiveSegments counts the currently transferring segments.
	ActiveSegments int `json:"active_segments"`
	// Error describes the download failure.
	Error string `json:"error"`
	// ErrorKind is the stable failure category, or empty on success.
	ErrorKind string `json:"error_kind"`
	// Added is the queue timestamp in milliseconds since the Unix epoch.
	Added int64 `json:"added"`
}

func jobsJSON(jobs []app.Job) []jobJSON {
	out := make([]jobJSON, 0, len(jobs))
	for _, j := range jobs {
		kind := ""
		if j.Err != "" {
			kind = j.ErrKind.String()
		}
		out = append(out, jobJSON{
			ID:             j.ID,
			Server:         j.Server,
			Remote:         j.Remote,
			Local:          j.Local,
			State:          j.State.String(),
			Size:           j.Size,
			Done:           j.Done,
			Speed:          j.Speed,
			Segments:       j.Segments,
			ActiveSegments: j.ActiveSegments,
			Error:          j.Err,
			ErrorKind:      kind,
			Added:          j.Added.UnixMilli(),
		})
	}
	return out
}
