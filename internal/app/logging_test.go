package app

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"spiid/internal/config"
	"spiid/internal/logx"
	"spiid/internal/sftptest"
)

func TestDownloadLogging(t *testing.T) {
	e := newEnv(t, sftptest.Options{})
	e.remote("file.bin", 10000)
	var b bytes.Buffer
	s, err := New(Options{Home: e.home, ConfigDir: e.cfg, KnownHosts: e.known, Prompter: e.p, Log: logx.New(&b, true)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.SaveSettings(config.Settings{
		DownloadDir: e.dl, ThreadsPerFile: 2, ParallelFiles: 1, MaxConnections: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err = s.AddServer(context.Background(), e.server(config.AuthPassword), "secret"); err != nil {
		t.Fatal(err)
	}
	if err = s.Download("box", []string{"/file.bin"}, false); err != nil {
		t.Fatal(err)
	}
	settle(t, s, 1)
	s.Close()
	for _, want := range []string{"download: added", "download: started", "download: done"} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("missing %s: %s", want, b.String())
		}
	}
	for _, line := range strings.Split(b.String(), "\n") {
		if strings.Contains(line, "secret") {
			t.Fatalf("secret leaked: %s", line)
		}
	}
}
