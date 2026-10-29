package main

import (
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

var openBrowser = func(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Run()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Run()
	default:
		return exec.Command("xdg-open", url).Run()
	}
}

// browserFile keeps the login fragment out of the opener's command line.
func browserFile(url string, done <-chan struct{}) (string, error) {
	dir, err := os.MkdirTemp("", "spiid-browser-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "open.html")
	content := `<!doctype html><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=` +
		html.EscapeString(url) + `">`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	go func() {
		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-done:
		}
		os.RemoveAll(dir)
	}()
	return path, nil
}
