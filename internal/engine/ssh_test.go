package engine

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"spiid/internal/config"
	"spiid/internal/sftptest"
	"spiid/internal/sshconn"
)

// sshPools adapts *sshconn.Manager to PoolSource.
type sshPools struct{ m *sshconn.Manager }

func (s sshPools) Pool(srv config.Server, maxConns int) Pool { return s.m.Pool(srv, maxConns) }

// noPrompts fails the test on any prompt.
type noPrompts struct{ t *testing.T }

func (p noPrompts) ConfirmHostKey(context.Context, sshconn.HostKeyRequest) (bool, error) {
	p.t.Error("unexpected host key prompt")
	return false, errors.New("unexpected")
}

func (p noPrompts) Secret(context.Context, sshconn.SecretRequest) (sshconn.SecretAnswer, error) {
	p.t.Error("unexpected secret prompt")
	return sshconn.SecretAnswer{}, errors.New("unexpected")
}

func TestRealSSHDownload(t *testing.T) {
	home := t.TempDir()
	root, dest := t.TempDir(), t.TempDir()
	ss := sftptest.Start(t, root, sftptest.Options{User: "alice", Password: "secret"})
	known := filepath.Join(home, "known_hosts")
	if err := os.WriteFile(known, []byte(ss.KnownHostsLine()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	creds, err := config.Open(filepath.Join(home, "config.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := creds.SaveServers([]config.Server{{Name: "box", Host: ss.Host(), Port: ss.Port(), User: "alice",
		Auth: config.AuthPassword}}); err != nil {
		t.Fatal(err)
	}
	if err := creds.Set("box", config.Password, "secret"); err != nil {
		t.Fatal(err)
	}
	sm := sshconn.NewManager(sshconn.Options{
		Home: home, KnownHosts: known, Credentials: creds, Prompter: noPrompts{t},
		DialTimeout: 5 * time.Second,
		AgentConn:   func() (io.ReadWriteCloser, error) { return nil, errors.New("no agent in tests") },
	})
	t.Cleanup(sm.Close)

	e := &env{t: t, remote: root, dest: dest, srv: config.Server{
		Name: "box", Host: ss.Host(), Port: ss.Port(), User: "alice", Auth: config.AuthPassword,
	}}
	want := e.remoteFile("real.bin", 3<<20)
	e.m = e.newManager(func(o *Options) { o.Pools = sshPools{sm}; o.MinSegment = 256 << 10 })
	j := e.waitState(e.add("real.bin").ID, Done)
	if j.Segments != 4 {
		t.Errorf("Segments = %d, want 4", j.Segments)
	}
	equalFile(t, e.req("real.bin").Local, want)
}
