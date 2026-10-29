package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"spiid/internal/errs"
)

func TestKDFBounds(t *testing.T) {
	valid := defaultKDF
	valid.Salt = make([]byte, 16)
	valid.Check = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=="
	if err := valid.validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*masterParams){
		func(m *masterParams) { m.Time = 0 }, func(m *masterParams) { m.Time = 11 },
		func(m *masterParams) { m.MemoryKiB = 8191 }, func(m *masterParams) { m.MemoryKiB = 1048577 },
		func(m *masterParams) { m.Threads = 0 }, func(m *masterParams) { m.Threads = 17 },
		func(m *masterParams) { m.Salt = nil }, func(m *masterParams) { m.Check = "!" },
	} {
		m := valid
		mutate(&m)
		if err := m.validate(); err == nil {
			t.Fatalf("accepted invalid KDF: %+v", m)
		}
	}
}
func TestMalformedConfig(t *testing.T) {
	s := secretStore(t)
	original, _ := json.Marshal(s.file)
	for _, data := range [][]byte{nil, []byte("{}"), []byte("null"), original[:len(original)-1],
		append(append([]byte(nil), original...), []byte(" {}")...)} {
		path := filepath.Join(s.dir, "config.json")
		os.WriteFile(path, data, 0600)
		if _, err := Open(path, nil); err == nil {
			t.Fatalf("accepted malformed config %s", data)
		}
	}
	for _, name := range []string{"version", "settings", "servers", "master"} {
		var fields map[string]json.RawMessage
		json.Unmarshal(original, &fields)
		delete(fields, name)
		data, _ := json.Marshal(fields)
		path := filepath.Join(s.dir, "config.json")
		os.WriteFile(path, data, 0600)
		if _, err := Open(path, nil); err == nil {
			t.Fatalf("accepted missing %s", name)
		}
	}
}
func TestRelativeDownloadDirRejected(t *testing.T) {
	s := secretStore(t)
	st, _ := s.LoadSettings()
	st.DownloadDir = "relative/folder"
	if err := s.SaveSettings(st); errs.KindOf(err) != errs.InvalidInput {
		t.Fatal(err)
	}
	st.DownloadDir = "~/downloads"
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if s.file.Settings.DownloadDir != filepath.Join(s.home, "downloads") {
		t.Fatal("home not expanded")
	}
}
func TestConcurrentConfigUpdates(t *testing.T) {
	s := secretStore(t)
	st, _ := s.LoadSettings()
	st.Theme = ThemeDark
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Set("box", Password, "concurrent-value"); err != nil {
				t.Error(err)
			}
			if err := s.SaveSettings(st); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	next := reopen(t, s)
	requireSecret(t, next, "box", "concurrent-value")
	if next.file.Settings.Theme != ThemeDark {
		t.Fatal("lost settings")
	}
}
