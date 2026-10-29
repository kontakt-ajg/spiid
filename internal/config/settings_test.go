package config

import (
	"path/filepath"
	"testing"
)

func TestSettingsNormalize(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	def := DefaultSettings(home)

	var z Settings
	z.Normalize(home)
	if z != def {
		t.Errorf("zeros: got %+v, want %+v", z, def)
	}

	low := Settings{
		DownloadDir:    "~/dl/../d",
		ThreadsPerFile: -5,
		ParallelFiles:  -1,
		MaxConnections: -9,
		Theme:          "pink",
	}
	low.Normalize(home)
	want := Settings{DownloadDir: filepath.Join(home, "d"), ThreadsPerFile: 1, ParallelFiles: 1, MaxConnections: 1,
		Theme: ThemeSystem}
	if low != want {
		t.Errorf("low: got %+v, want %+v", low, want)
	}

	high := Settings{
		DownloadDir:    "/a/b/",
		ThreadsPerFile: 100,
		ParallelFiles:  100,
		MaxConnections: 100,
		Theme:          ThemeDark,
	}
	high.Normalize(home)
	want = Settings{DownloadDir: filepath.Clean("/a/b"), ThreadsPerFile: 32, ParallelFiles: 16, MaxConnections: 64,
		Theme: ThemeDark}
	if high != want {
		t.Errorf("high: got %+v, want %+v", high, want)
	}

	in := Settings{ThreadsPerFile: 7, ParallelFiles: 5, MaxConnections: 9, Theme: ThemeLight}
	in.Normalize(home)
	if in.ThreadsPerFile != 7 || in.ParallelFiles != 5 || in.MaxConnections != 9 || in.Theme != ThemeLight {
		t.Errorf("in range changed: %+v", in)
	}
}
