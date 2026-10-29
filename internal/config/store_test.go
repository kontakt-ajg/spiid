package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"spiid/internal/errs"
)

func TestDefaultDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("AppData", `C:\Users\x\AppData\Roaming`)
		got, err := DefaultDir("home")
		if err != nil || got != `C:\Users\x\AppData\Roaming\spiid` {
			t.Errorf("got %q, %v", got, err)
		}
		t.Setenv("AppData", "")
		if _, err := DefaultDir("home"); err == nil {
			t.Error("expected error")
		}
		return
	}
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if got, _ := DefaultDir(home); got != filepath.Join(xdg, "spiid") {
		t.Errorf("xdg: %q", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "relative/path")
	if got, _ := DefaultDir(home); got != filepath.Join(home, ".config", "spiid") {
		t.Errorf("relative: %q", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	if got, _ := DefaultDir(home); got != filepath.Join(home, ".config", "spiid") {
		t.Errorf("unset: %q", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".config")); err == nil {
		t.Error("DefaultDir created something")
	}
}

func TestServersRoundTrip(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(t.TempDir(), "cfg")
	st := NewStore(dir, home)
	in := []Server{
		{Name: "one", Host: "h1", Port: 22, User: "u", Auth: AuthPassword, StartDir: "/data"},
		{Name: "two", Host: "h2", Port: 2222, User: "v", Auth: AuthKey, KeyFile: "~/.ssh/id"},
		{Name: "three", Host: "h3", Port: 22, User: "w", Auth: AuthAgent},
	}
	if err := st.SaveServers(in); err != nil {
		t.Fatal(err)
	}
	out, err := st.LoadServers()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("got %+v", out)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if !strings.Contains(string(data), `"servers": [`) || !strings.HasSuffix(string(data), "}\n") {
		t.Errorf("bad json:\n%s", data)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(filepath.Join(dir, "config.json"))
		di, _ := os.Stat(dir)
		if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
			t.Errorf("modes file %v dir %v", fi.Mode().Perm(), di.Mode().Perm())
		}
	}
	if err := st.SaveServers(nil); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(dir, "config.json"))
	if !strings.Contains(string(data), "[]") {
		t.Errorf("empty list: %s", data)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	home := t.TempDir()
	st := NewStore(filepath.Join(t.TempDir(), "cfg"), home)
	in := Settings{DownloadDir: filepath.Join(home, "x"), ThreadsPerFile: 5, ParallelFiles: 2, MaxConnections: 6,
		Theme: ThemeDark}
	if err := st.SaveSettings(in); err != nil {
		t.Fatal(err)
	}
	out, err := st.LoadSettings()
	if err != nil || out != in {
		t.Errorf("got %+v, %v", out, err)
	}
}

func TestSaveServersDuplicateName(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "cfg"), t.TempDir())
	err := st.SaveServers([]Server{
		{Name: "A", Host: "h", User: "u", Auth: AuthAgent},
		{Name: "a", Host: "h", User: "u", Auth: AuthAgent},
	})
	if errs.KindOf(err) != errs.InvalidInput || !strings.Contains(err.Error(), "used twice") {
		t.Errorf("got %v", err)
	}
	if _, serr := os.Stat(st.Dir()); serr == nil {
		t.Error("dir created despite error")
	}
}

func TestLoadMissingFiles(t *testing.T) {
	home := t.TempDir()
	st := NewStore(filepath.Join(t.TempDir(), "none"), home)
	servers, err := st.LoadServers()
	if len(servers) != 0 || err != nil {
		t.Errorf("servers %v, %v", servers, err)
	}
	s, err := st.LoadSettings()
	if err != nil || s != DefaultSettings(home) {
		t.Errorf("settings %+v, %v", s, err)
	}
}

func TestLoadBadJSON(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir, t.TempDir())
	for _, name := range []string{"config.json", "config.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{nope"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, err := st.LoadServers()
	if errs.KindOf(err) != errs.InvalidInput || !strings.Contains(err.Error(), "config.json") {
		t.Errorf("servers: %v", err)
	}
	_, err = st.LoadSettings()
	if errs.KindOf(err) != errs.InvalidInput || !strings.Contains(err.Error(), "config.json") {
		t.Errorf("settings: %v", err)
	}
}

func TestSaveServersEqualFoldDuplicate(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "cfg"), t.TempDir())
	if !strings.EqualFold("S", "ſ") {
		t.Fatal("probe precondition")
	}
	err := s.SaveServers([]Server{
		{Name: "S", Host: "h", User: "u", Auth: AuthAgent},
		{Name: "ſ", Host: "h", User: "u", Auth: AuthAgent},
	})
	if errs.KindOf(err) != errs.InvalidInput {
		t.Fatalf("EqualFold duplicate accepted: err=%v", err)
	}
}
