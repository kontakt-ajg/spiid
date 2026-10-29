package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStateValidation(t *testing.T) {
	const size = 100
	mtime := time.Unix(1000, 0)
	good := func() stateFile {
		return stateFile{
			RemotePath: "/r",
			Size:       size,
			MTime:      1000,
			Source:     source{"h", 22, "u"},
			Segments:   []Segment{{0, 50, 10}, {50, 100, 50}},
		}
	}
	cases := []struct {
		name     string
		mod      func(*stateFile)
		partSize int64
		raw      string
		valid    bool
	}{
		{"good", func(*stateFile) {}, size, "", true},
		{"all done", func(s *stateFile) { s.Segments[0].Done, s.Segments[1].Done = 50, 50 }, size, "", true},
		{"other remote", func(s *stateFile) { s.RemotePath = "/x" }, size, "", false},
		{"other size", func(s *stateFile) { s.Size = 99 }, size, "", false},
		{"other host", func(s *stateFile) { s.Source.Host = "x" }, size, "", false},
		{"other port", func(s *stateFile) { s.Source.Port = 23 }, size, "", false},
		{"other user", func(s *stateFile) { s.Source.User = "x" }, size, "", false},
		{
			"no source",
			nil,
			size,
			`{"remote_path":"/r","size":100,"mtime":1000,"segments":[{"start":0,"end":100,"done":0}]}`,
			false,
		},
		{"other mtime", func(s *stateFile) { s.MTime = 1001 }, size, "", false},
		{"part too short", func(*stateFile) {}, size - 1, "", false},
		{"part too long", func(*stateFile) {}, size + 1, "", false},
		{"no segments", func(s *stateFile) { s.Segments = nil }, size, "", false},
		{"gap", func(s *stateFile) { s.Segments[1].Start = 51 }, size, "", false},
		{"overlap", func(s *stateFile) { s.Segments[1].Start = 49 }, size, "", false},
		{
			"unsorted",
			func(s *stateFile) { s.Segments[0], s.Segments[1] = s.Segments[1], s.Segments[0] },
			size,
			"",
			false,
		},
		{"not from 0", func(s *stateFile) { s.Segments[0].Start = 1 }, size, "", false},
		{"short of size", func(s *stateFile) { s.Segments[1].End = 99 }, size, "", false},
		{"past size", func(s *stateFile) { s.Segments[1].End = 101 }, size, "", false},
		{"negative done", func(s *stateFile) { s.Segments[0].Done = -1 }, size, "", false},
		{"done too big", func(s *stateFile) { s.Segments[0].Done = 51 }, size, "", false},
		{"empty segment", func(s *stateFile) { s.Segments[0].End = 0 }, size, "", false},
		{"not json", nil, size, "{nope", false},
		{
			"truncated",
			nil,
			size,
			`{"remote_path":"/r","size":100,"mtime":1000,"source":{"host":"h","port":22,` +
				`"user":"u"},"segments":[{"start":0,"end":100,"done":0}`,
			false,
		},
		{
			"missing field",
			nil,
			size,
			`{"remote_path":"/r","size":100,"segments":[{"start":0,"end":100,"done":0}]}`,
			false,
		},
		{
			"missing segment field",
			nil,
			size,
			`{"remote_path":"/r","size":100,"mtime":1000,"source":{"host":"h","port":22,` +
				`"user":"u"},"segments":[{"start":0,"end":100}]}`,
			false,
		},
		{
			"trailing bytes",
			nil,
			size,
			`{"remote_path":"/r","size":100,"mtime":1000,"source":{"host":"h","port":22,` +
				`"user":"u"},"segments":[{"start":0,"end":100,"done":0}]} x`,
			false,
		},
		{
			"second document",
			nil,
			size,
			`{"remote_path":"/r","size":100,"mtime":1000,"source":{"host":"h","port":22,` +
				`"user":"u"},"segments":[{"start":0,"end":100,"done":0}]}{}`,
			false,
		},
		{
			"valid raw",
			nil,
			size,
			`{"remote_path":"/r","size":100,"mtime":1000,"source":{"host":"h","port":22,` +
				`"user":"u"},"segments":[{"start":0,"end":100,"done":7}]}`,
			true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			state, part := filepath.Join(dir, "f.spiid-state"), filepath.Join(dir, "f.spiid-part")
			data := []byte(c.raw)
			if c.mod != nil {
				st := good()
				c.mod(&st)
				data, _ = json.Marshal(st)
			}
			os.WriteFile(state, data, 0o600)
			os.WriteFile(part, make([]byte, c.partSize), 0o600)
			segs, ok := loadState(state, part, "/r", size, mtime, source{"h", 22, "u"})
			if ok != c.valid {
				t.Fatalf("valid = %v, want %v (segs %+v)", ok, c.valid, segs)
			}
			if ok && len(segs) == 0 {
				t.Fatal("valid state without segments")
			}
		})
	}
	t.Run("no part file", func(t *testing.T) {
		dir := t.TempDir()
		data, _ := json.Marshal(good())
		os.WriteFile(filepath.Join(dir, "s"), data, 0o600)
		if _, ok := loadState(filepath.Join(dir, "s"), filepath.Join(dir, "missing"), "/r", size, mtime,
			source{"h", 22, "u"}); ok {
			t.Fatal("state without part file must be invalid")
		}
	})
	t.Run("no state file", func(t *testing.T) {
		dir := t.TempDir()
		if _, ok := loadState(filepath.Join(dir, "s"), filepath.Join(dir, "p"), "/r", size, mtime, source{"h", 22, "u"}); ok {
			t.Fatal("missing state must be invalid")
		}
	})
}
