package app

import (
	"context"
	"io"
	"net"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"spiid/internal/config"
	"spiid/internal/errs"
)

func TestNewDefaultsAndVersion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	s, err := New(Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Version() != "0.1.0" {
		t.Fatalf("version %q", s.Version())
	}
	if want := filepath.Join(home, ".config", "spiid"); s.ConfigDir() != want {
		t.Fatalf("config dir %q, want %q", s.ConfigDir(), want)
	}
	if got := s.Settings().DownloadDir; got != filepath.Join(home, "Downloads") {
		t.Fatalf("download dir %q", got)
	}
	s.Close() // idempotent
}

func TestNewReturnsLoadErrors(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "cfg")
	writeFile(t, filepath.Join(cfg, "config.json"), "{not json")
	if _, err := New(Options{Home: home, ConfigDir: cfg}); err == nil {
		t.Fatal("want an error for a broken config.json")
	}
	writeFile(t, filepath.Join(cfg, "config.json"), "{}")
	writeFile(t, filepath.Join(cfg, "config.json"), "{not json")
	if _, err := New(Options{Home: home, ConfigDir: cfg}); err == nil {
		t.Fatal("want an error for a broken config.json")
	}
}

func TestNilPrompterCancels(t *testing.T) {
	var p nilPrompter
	if _, err := p.ConfirmHostKey(context.Background(), HostKeyRequest{}); errs.KindOf(err) != errs.Canceled {
		t.Fatalf("host key: %v", err)
	}
	if _, err := p.Secret(context.Background(), SecretRequest{}); errs.KindOf(err) != errs.Canceled {
		t.Fatalf("secret: %v", err)
	}
}

func TestDownloadAfterClose(t *testing.T) {
	e := newEnv(t, sftpOpts())
	s := e.ready(1, 1)
	if err := s.AddServer(context.Background(), e.server(config.AuthAgent), ""); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err := s.Download("box", []string{"/x"}, false); errs.KindOf(err) != errs.Canceled {
		t.Fatalf("got %v", err)
	}
}

func TestCloseCancelsBlockedWalkSend(t *testing.T) { testBlockedWalk(t, false) }

func TestCancelScanBlockedWalkSend(t *testing.T) { testBlockedWalk(t, true) }

func testBlockedWalk(t *testing.T, cancelScan bool) {
	_, pub := newKey(t)
	_ = pub
	priv, _ := newKey(t)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	release := make(chan struct{})
	ready := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		nc, err := ln.Accept()
		if err != nil {
			return
		}
		defer nc.Close()
		cfg := &ssh.ServerConfig{NoClientAuth: true}
		cfg.AddHostKey(signer)
		sc, chs, rs, err := ssh.NewServerConn(nc, cfg)
		if err != nil {
			return
		}
		defer sc.Close()
		go ssh.DiscardRequests(rs)
		for nch := range chs {
			ch, reqs, err := nch.Accept()
			if err != nil {
				return
			}
			for req := range reqs {
				if req.Type == "subsystem" {
					req.Reply(true, nil)
					b := make([]byte, 9)
					if _, err := io.ReadFull(ch, b); err != nil {
						return
					}
					ch.Write([]byte{0, 0, 0, 5, 2, 0, 0, 0, 3})
					close(ready)
					<-release
					ch.Close()
					return
				}
				req.Reply(false, nil)
			}
		}
	}()
	home := t.TempDir()
	p := &scriptPrompter{t: t, password: "secret"}
	s, err := New(Options{Home: home, ConfigDir: filepath.Join(home, "cfg"), Prompter: p})
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	srv := config.Server{
		Name: "box",
		Host: "127.0.0.1",
		Port: ln.Addr().(*net.TCPAddr).Port,
		User: "alice",
		Auth: config.AuthPassword,
	}
	if err := s.AddServer(context.Background(), srv, "pw"); err != nil {
		close(release)
		t.Fatal(err)
	}
	if err := s.Download("box", []string{"/" + strings.Repeat("x", 4<<20)}, false); err != nil {
		close(release)
		t.Fatal(err)
	}
	<-ready
	deadline := time.Now().Add(5 * time.Second)
	for {
		stack := make([]byte, 1<<20)
		n := runtime.Stack(stack, true)
		if strings.Contains(string(stack[:n]), "(*Client).send") {
			break
		}
		if time.Now().After(deadline) {
			close(release)
			s.Close()
			t.Fatal("walk was not blocked in send")
		}
		time.Sleep(5 * time.Millisecond)
	}
	done := make(chan struct{})
	if cancelScan {
		scans := s.Scans()
		if len(scans) != 1 || scans[0].Folder == "" || scans[0].Server != "box" {
			t.Fatalf("scans: %+v", scans)
		}
		if err := s.CancelScan(scans[0].ID); err != nil {
			t.Fatal(err)
		}
		go func() { s.walks.Wait(); close(done) }()
	} else {
		go func() { s.Close(); close(done) }()
	}

	blocked := false
	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		blocked = true
	}
	close(release)
	<-done
	if cancelScan {
		if len(s.Scans()) != 0 {
			t.Fatal("scan retained")
		}
		if ms := messageTexts(s); len(ms) != 1 || !strings.Contains(ms[0], " canceled: 0 files queued") {
			t.Fatalf("%v", ms)
		}
		s.Close()
	}
	<-serverDone
	if blocked {
		t.Fatal("Close blocked on walk send despite canceled lifetime; returned only after server released transport")
	}
}
