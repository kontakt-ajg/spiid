package config

import (
	"path/filepath"

	"spiid/internal/fsutil"
)

// Theme is the GUI colour scheme choice.
type Theme string

// The themes.
const (
	ThemeSystem Theme = "system"
	ThemeLight  Theme = "light"
	ThemeDark   Theme = "dark"
)

// Settings are the user's preferences.
type Settings struct {
	DownloadDir    string `json:"download_dir"`     // default <home>/Downloads
	ThreadsPerFile int    `json:"threads_per_file"` // 1..32, default 4
	ParallelFiles  int    `json:"parallel_files"`   // 1..16, default 3
	MaxConnections int    `json:"max_connections"`  // 1..64, default 8
	Theme          Theme  `json:"theme"`            // default system
}

// DefaultSettings returns the defaults for the given home folder.
func DefaultSettings(home string) Settings {
	return Settings{
		DownloadDir:    filepath.Join(home, "Downloads"),
		ThreadsPerFile: 4,
		ParallelFiles:  3,
		MaxConnections: 8,
		Theme:          ThemeSystem,
	}
}

// Normalize applies defaults to zero values and clamps everything to its range.
func (s *Settings) Normalize(home string) {
	def := DefaultSettings(home)
	if s.DownloadDir == "" {
		s.DownloadDir = def.DownloadDir
	} else {
		s.DownloadDir = filepath.Clean(fsutil.ExpandHome(s.DownloadDir, home))
	}
	s.ThreadsPerFile = clamp(s.ThreadsPerFile, def.ThreadsPerFile, 1, 32)
	s.ParallelFiles = clamp(s.ParallelFiles, def.ParallelFiles, 1, 16)
	s.MaxConnections = clamp(s.MaxConnections, def.MaxConnections, 1, 64)
	switch s.Theme {
	case ThemeSystem, ThemeLight, ThemeDark:
	default:
		s.Theme = ThemeSystem
	}
}

func clamp(v, def, lo, hi int) int {
	switch {
	case v == 0:
		return def
	case v < lo:
		return lo
	case v > hi:
		return hi
	}
	return v
}
