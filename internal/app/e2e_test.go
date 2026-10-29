package app

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"spiid/internal/config"
	"spiid/internal/engine"
	"spiid/internal/errs"
	"spiid/internal/sftptest"
	"spiid/internal/sshconn"
)

func TestE2EHostKeyAndPasswordOnce(t *testing.T) {
	e := newEnv(t, sftptest.Options{})
	want := map[string][]byte{}
	for i := 0; i < 6; i++ {
		name := "f" + string(rune('0'+i)) + ".bin"
		want[name] = e.remote("dir/"+name, 5<<20)
	}
	s := e.ready(4, 3)
	if err := s.AddServer(context.Background(), e.server(config.AuthPassword), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Download("box", []string{"/dir"}, false); err != nil {
		t.Fatal(err)
	}
	for _, j := range settle(t, s, 6) {
		if j.State != engine.Done {
			t.Fatalf("job %+v", j)
		}
	}
	if h, p := e.p.hostCount(), e.p.count(sshconn.SecretPassword); h != 1 || p != 1 {
		t.Fatalf("host prompts %d, password prompts %d; want 1 and 1", h, p)
	}
	for name, data := range want {
		e.mustHave("dir/"+name, data)
	}
	kh, err := os.ReadFile(e.known)
	if err != nil || !strings.Contains(string(kh), e.ss.KnownHostsLine()) {
		t.Fatalf("known_hosts %q, %v", kh, err)
	}
}

func TestE2EFolderTree(t *testing.T) {
	e := newEnv(t, sftptest.Options{})
	top := e.remote("tree/top.txt", 100)
	deep := e.remote("tree/sub/deep/f.txt", 2000)
	e.remote("tree/a:b", 10)
	if err := os.Symlink("top.txt", filepath.Join(e.root, "tree", "link")); err != nil {
		t.Fatal(err)
	}
	s := e.ready(2, 2)
	e.trust()
	if err := s.AddServer(context.Background(), e.server(config.AuthPassword), "secret"); err != nil {
		t.Fatal(err)
	}
	if err := s.Download("box", []string{"/tree"}, false); err != nil {
		t.Fatal(err)
	}
	for _, j := range settle(t, s, 2) {
		if j.State != engine.Done {
			t.Fatalf("job %+v", j)
		}
	}
	e.mustHave("tree/top.txt", top)
	e.mustHave("tree/sub/deep/f.txt", deep)
	if len(s.Jobs()) != 2 {
		t.Fatalf("%d jobs, want 2", len(s.Jobs()))
	}
	msgs := strings.Join(messageTexts(s), "\n")
	if !strings.Contains(msgs, "/tree/link: symlink") || !strings.Contains(msgs, "/tree/a:b: unsafe name") {
		t.Fatalf("messages:\n%s", msgs)
	}
}

func TestE2EDuplicateTargets(t *testing.T) {
	e := newEnv(t, sftptest.Options{})
	a := e.remote("x/a.bin", 500)
	e.remote("y/A.BIN", 700)
	s := e.ready(2, 2)
	e.trust()
	if err := s.AddServer(context.Background(), e.server(config.AuthPassword), "secret"); err != nil {
		t.Fatal(err)
	}
	if err := s.Download("box", []string{"/x/a.bin", "/y/A.BIN"}, false); err != nil {
		t.Fatal(err)
	}
	s.walks.Wait()
	settle(t, s, 1)
	e.mustHave("a.bin", a)
	if n := len(s.Jobs()); n != 1 {
		t.Fatalf("%d jobs, want 1", n)
	}
	dups := 0
	for _, m := range messageTexts(s) {
		if strings.Contains(m, "duplicate") {
			dups++
		}
	}
	if dups != 1 {
		t.Fatalf("%d duplicate messages: %v", dups, messageTexts(s))
	}
}

func TestE2EResumeAfterDrop(t *testing.T) {
	e := newEnv(t, sftptest.Options{ReadDelay: 10 * time.Millisecond})
	data := e.remote("big.bin", 3<<20)
	s := e.ready(1, 1)
	e.trust()
	if err := s.AddServer(context.Background(), e.server(config.AuthPassword), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Download("box", []string{"/big.bin"}, false); err != nil {
		t.Fatal(err)
	}
	js := waitFor(t, s, "partial job", func(js []Job) bool { return len(js) == 1 && js[0].Done > 0 })
	if j := js[0]; j.State != engine.Running || j.Done >= j.Size {
		t.Fatalf("job is not partial: %+v", j)
	}
	e.ss.SetReadDelay(0)
	e.ss.DropAll()
	js = settle(t, s, 1)
	if js[0].State != engine.Done {
		t.Fatalf("job %+v", js[0])
	}
	e.mustHave("big.bin", data)
	if n := e.p.count(sshconn.SecretPassword); n != 1 {
		t.Fatalf("%d password prompts, want 1", n)
	}
}

func TestE2ECancelRetry(t *testing.T) {
	e := newEnv(t, sftptest.Options{ReadDelay: 10 * time.Millisecond})
	data := e.remote("big.bin", 3<<20)
	s := e.ready(1, 1)
	// A single connection makes cancellation discard the only authenticated transport.
	st := s.Settings()
	st.MaxConnections = 1
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	e.trust()
	if err := s.AddServer(context.Background(), e.server(config.AuthPassword), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Download("box", []string{"/big.bin"}, false); err != nil {
		t.Fatal(err)
	}
	js := waitFor(t, s, "partial job", func(js []Job) bool { return len(js) == 1 && js[0].Done > 0 })
	id := js[0].ID
	if j := js[0]; j.State != engine.Running || j.Done >= j.Size {
		t.Fatalf("job is not partial: %+v", j)
	}
	if err := s.Cancel(id); err != nil {
		t.Fatal(err)
	}
	js = settle(t, s, 1)
	if js[0].State != engine.Canceled || js[0].Done >= js[0].Size {
		t.Fatalf("job %+v", js[0])
	}
	if _, err := os.Stat(js[0].Local + ".spiid-state"); err != nil {
		t.Fatalf("state file: %v", err)
	}
	e.ss.SetReadDelay(0)
	if err := s.Retry(id); err != nil {
		t.Fatal(err)
	}
	js = settle(t, s, 1)
	if js[0].State != engine.Done {
		t.Fatalf("job %+v", js[0])
	}
	e.mustHave("big.bin", data)
	if n := e.p.count(sshconn.SecretPassword); n != 2 {
		t.Fatalf("%d password prompts, want 2 (Retry forgets the session secret)", n)
	}
}

func TestE2ELocalExistsOverwrite(t *testing.T) {
	e := newEnv(t, sftptest.Options{})
	data := e.remote("f.bin", 4000)
	s := e.ready(2, 2)
	e.trust()
	if err := s.AddServer(context.Background(), e.server(config.AuthPassword), "secret"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(e.dl, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.dl, "f.bin"), []byte("different"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Download("box", []string{"/f.bin"}, false); err != nil {
		t.Fatal(err)
	}
	js := settle(t, s, 1)
	if js[0].State != engine.Failed || js[0].ErrKind != errs.LocalExists {
		t.Fatalf("job %+v", js[0])
	}
	if err := s.Overwrite(js[0].ID); err != nil {
		t.Fatal(err)
	}
	js = settle(t, s, 1)
	if js[0].State != engine.Done {
		t.Fatalf("job %+v", js[0])
	}
	e.mustHave("f.bin", data)
}

func TestE2EMasterPassword(t *testing.T) {
	e := newEnv(t, sftptest.Options{})
	data := e.remote("f.bin", 4000)
	e.trust()
	s := e.ready(2, 2)
	if err := s.Protect(context.Background(), "master-password"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddServer(context.Background(), e.server(config.AuthPassword), "secret"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if e.p.count(sshconn.SecretMaster) != 0 {
		t.Fatal("the first service should not have asked for the master password")
	}

	s2 := e.service()
	if !s2.CredentialsEncrypted() {
		t.Fatal("credentials should be encrypted")
	}
	if err := s2.Download("box", []string{"/f.bin"}, false); err != nil {
		t.Fatal(err)
	}
	if js := settle(t, s2, 1); js[0].State != engine.Done {
		t.Fatalf("job %+v", js[0])
	}
	e.mustHave("f.bin", data)
	if m, p := e.p.count(sshconn.SecretMaster), e.p.count(sshconn.SecretPassword); m != 1 || p != 0 {
		t.Fatalf("master prompts %d, password prompts %d; want 1 and 0", m, p)
	}
}

func TestE2EBrowseList(t *testing.T) {
	e := newEnv(t, sftptest.Options{})
	e.remote("b.txt", 5)
	e.remote("A.txt", 7)
	e.remote("Beta/in.txt", 1)
	e.remote("alpha/in.txt", 1)
	e.remote("sub/x.txt", 3)
	if err := os.Symlink("alpha", filepath.Join(e.root, "lnkdir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("b.txt", filepath.Join(e.root, "lnkfile")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("nowhere", filepath.Join(e.root, "broken")); err != nil {
		t.Fatal(err)
	}
	s := e.ready(2, 2)
	e.trust()
	ctx := context.Background()
	if err := s.AddServer(ctx, e.server(config.AuthPassword), "secret"); err != nil {
		t.Fatal(err)
	}
	l, err := s.List(ctx, "box", "")
	if err != nil {
		t.Fatal(err)
	}
	if l.Path != "/" {
		t.Fatalf("path %q", l.Path)
	}
	var names []string
	for _, en := range l.Entries {
		names = append(names, en.Name)
	}
	if got, want := strings.Join(names, ","), "alpha,Beta,lnkdir,sub,A.txt,b.txt,broken,lnkfile"; got != want {
		t.Fatalf("order %s, want %s", got, want)
	}
	by := map[string]Entry{}
	for _, en := range l.Entries {
		by[en.Name] = en
	}
	if d := by["lnkdir"]; !d.IsLink || !d.IsDir || d.Path != "/lnkdir" {
		t.Fatalf("lnkdir %+v", d)
	}
	if f := by["lnkfile"]; !f.IsLink || f.IsDir || f.Size != 5 {
		t.Fatalf("lnkfile %+v", f)
	}
	if b := by["broken"]; !b.IsLink || b.IsDir {
		t.Fatalf("broken %+v", b)
	}
	if f := by["A.txt"]; f.IsLink || f.IsDir || f.Size != 7 || f.ModTime.IsZero() {
		t.Fatalf("A.txt %+v", f)
	}
	sub, err := s.List(ctx, "box", "/sub")
	if err != nil || sub.Path != "/sub" || len(sub.Entries) != 1 || sub.Entries[0].Path != "/sub/x.txt" {
		t.Fatalf("sub %+v, %v", sub, err)
	}
	srv := e.server(config.AuthPassword)
	srv.StartDir = "/sub"
	if err := s.UpdateServer(ctx, "box", srv, nil); err != nil {
		t.Fatal(err)
	}
	if l, err = s.List(ctx, "box", ""); err != nil || l.Path != "/sub" {
		t.Fatalf("start dir listing %+v, %v", l, err)
	}
}

func TestE2EServerCRUD(t *testing.T) {
	e := newEnv(t, sftptest.Options{})
	s := e.service()
	ctx := context.Background()
	saved := func() []config.Server {
		t.Helper()
		list, err := config.NewStore(e.cfg, e.home).LoadServers()
		if err != nil {
			t.Fatal(err)
		}
		return list
	}
	secret := func(name string, k config.Kind) string {
		t.Helper()
		v, _, err := e.readCreds().Get(name, k)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if err := s.AddServer(ctx, e.server(config.AuthPassword), "pw1"); err != nil {
		t.Fatal(err)
	}
	if l := saved(); len(l) != 1 || l[0].Name != "box" || secret("box", config.Password) != "pw1" {
		t.Fatalf("after add: %+v", l)
	}
	renamed := e.server(config.AuthPassword)
	renamed.Name = "box2"
	if err := s.UpdateServer(ctx, "box", renamed, nil); err != nil {
		t.Fatal(err)
	}
	if l := saved(); len(l) != 1 || l[0].Name != "box2" || secret("box2", config.Password) != "pw1" ||
		secret("box", config.Password) != "" {
		t.Fatalf("after rename: %+v", l)
	}
	agentSrv := renamed
	agentSrv.Auth = config.AuthAgent
	if err := s.UpdateServer(ctx, "box2", agentSrv, nil); err != nil {
		t.Fatal(err)
	}
	if l := saved(); l[0].Auth != config.AuthAgent || secret("box2", config.Password) != "" {
		t.Fatalf("after agent: %+v", l)
	}
	if err := s.RemoveServer(ctx, "box2"); err != nil {
		t.Fatal(err)
	}
	if l := saved(); len(l) != 0 || len(s.Servers()) != 0 {
		t.Fatalf("after remove: %+v", l)
	}
	if b, err := os.ReadFile(filepath.Join(e.cfg, "config.json")); err != nil || strings.Contains(string(b), "pw1") {
		t.Fatalf("credentials still hold the secret: %s %v", b, err)
	}
}

func TestE2EAgentLoginNoMasterPrompt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("isolated SSH_AUTH_SOCK agent requires Unix sockets")
	}
	priv, pub := newKey(t)
	e := newEnv(t, sftptest.Options{User: "alice", Password: "unused", AuthorizedKeys: []ssh.PublicKey{pub}})
	data := e.remote("f.bin", 4000)
	e.trust()
	kr := agent.NewKeyring()
	if err := kr.Add(agent.AddedKey{PrivateKey: priv}); err != nil {
		t.Fatal(err)
	}
	socketDir, err := os.MkdirTemp("/tmp", "spiid-agent-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(socketDir) })
	ln, err := net.Listen("unix", filepath.Join(socketDir, "agent.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	t.Setenv("SSH_AUTH_SOCK", ln.Addr().String())
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); agent.ServeAgent(kr, c) }()
		}
	}()
	c, err := config.Open(filepath.Join(e.cfg, "config.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Protect("master-password"); err != nil {
		t.Fatal(err)
	}
	s := e.ready(2, 2)
	if err := s.AddServer(context.Background(), e.server(config.AuthAgent), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Download("box", []string{"/f.bin"}, false); err != nil {
		t.Fatal(err)
	}
	if js := settle(t, s, 1); js[0].State != engine.Done {
		t.Fatalf("job %+v", js[0])
	}
	e.mustHave("f.bin", data)
	if m := e.p.count(sshconn.SecretMaster); m != 0 {
		t.Fatalf("%d master prompts, want 0", m)
	}
	if n := len(e.p.secrets); n != 0 {
		t.Fatalf("%d secret prompts, want 0", n)
	}
}
