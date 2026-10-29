package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"spiid/internal/app"
	"spiid/internal/logx"
	"spiid/internal/web"
)

func serve(port int, noBrowser, verbose bool) error {
	log := logx.New(os.Stderr, verbose)
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	s, err := web.Start(web.Options{App: app.Options{Log: log}, Port: port, Log: log})
	if err != nil {
		return fmt.Errorf("cannot listen on 127.0.0.1:%d: %w", port, err)
	}
	log.Info("config folder: %s", s.Service().ConfigDir())
	log.Info("download folder: %s", s.Service().Settings().DownloadDir)
	fmt.Printf("Spiid %s is running at %s — press Ctrl+C to quit\n", s.Service().Version(), s.URL())
	log.Info("started at http://127.0.0.1:%d/", s.Port())
	desktop := runtime.GOOS == "darwin" || runtime.GOOS == "windows" ||
		os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
	removeBrowserFile := func() {}
	if !noBrowser && desktop {
		cleanup := make(chan struct{})
		defer close(cleanup)
		path, err := browserFile(s.URL(), cleanup)
		if err != nil {
			log.Warn("could not open browser: %v; open the link above", err)
		} else {
			removeBrowserFile = func() { os.RemoveAll(filepath.Dir(path)) }
			defer removeBrowserFile()
			go func() {
				if err := openBrowser(path); err != nil {
					log.Warn("could not open browser: %v; open the link above", err)
				}
			}()
		}
	}
	reason := "quit from the web UI"
	select {
	case <-signals:
		reason = "Ctrl+C"
	case <-s.Done():
	}
	log.Info("stopping (%s)", reason)
	done := make(chan struct{})
	go func() { s.Close(); close(done) }()
	select {
	case <-signals:
		removeBrowserFile() // os.Exit skips deferred calls
		os.Exit(1)
	case <-done:
	}
	log.Info("stopped")
	return nil
}
