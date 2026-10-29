package web

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"

	"spiid/internal/errs"
)

type dirParams struct {
	// Dir selects the folder to list.
	Dir *string `json:"dir"`
}

type pathParams struct {
	// Path is the absolute local path to reveal.
	Path *string `json:"path"`
}

func localList(_ *Server, _ context.Context, p dirParams) (any, error) {
	dir := *p.Dir
	var err error
	if dir == "" {
		dir, err = os.UserHomeDir()
		if err != nil {
			return nil, err
		}
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type entry struct {
		// Name is the file or folder name.
		Name string `json:"name"`
		// Path is the absolute path.
		Path string `json:"path"`
		// IsDir reports whether the entry resolves to a directory.
		IsDir bool `json:"is_dir"`
	}
	out := make([]entry, 0, len(entries))
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if info, err := os.Stat(path); err == nil {
				isDir = info.IsDir()
			}
		}
		out = append(out, entry{e.Name(), path, isDir})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return out[i].Name < out[j].Name
	})
	parent := filepath.Dir(dir)
	if parent == dir {
		parent = ""
	}
	return struct {
		// Path is the absolute path.
		Path string `json:"path"`
		// Parent is the parent folder, or empty at the root.
		Parent string `json:"parent"`
		// Entries contains the folder entries.
		Entries []entry `json:"entries"`
	}{dir, parent, out}, nil
}

var reveal = func(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-R", "--", path)
	case "windows":
		cmd = windowsReveal(path)
	default:
		dir := path
		if info, err := os.Stat(path); err != nil {
			return err
		} else if !info.IsDir() {
			dir = filepath.Dir(path)
		}
		cmd = exec.Command("xdg-open", dir)
	}
	err := cmd.Run()
	if runtime.GOOS == "windows" {
		if e, ok := err.(*exec.ExitError); ok && e.ExitCode() == 1 {
			return nil
		}
	}
	return err
}

func localReveal(_ *Server, _ context.Context, p pathParams) (any, error) {
	if !filepath.IsAbs(*p.Path) {
		return nil, errs.New(errs.InvalidInput, "reveal", *p.Path, nil)
	}
	return empty{}, reveal(*p.Path)
}
