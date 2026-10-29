package sftptest

import (
	"net"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func dial(t *testing.T, s *Server, auth ssh.AuthMethod) (*ssh.Client, error) {
	t.Helper()
	cfg := &ssh.ClientConfig{
		User:            "alice",
		Auth:            []ssh.AuthMethod{auth},
		HostKeyCallback: ssh.FixedHostKey(s.HostKey()),
		Timeout:         5 * time.Second,
	}
	return ssh.Dial("tcp", net.JoinHostPort(s.Host(), strconv.Itoa(s.Port())), cfg)
}

func TestPasswordAuth(t *testing.T) {
	s := Start(t, t.TempDir(), Options{User: "alice", Password: "secret"})
	c, err := dial(t, s, ssh.Password("secret"))
	if err != nil {
		t.Fatalf("right password failed: %v", err)
	}
	c.Close()
	c, err = dial(t, s, ssh.Password("wrong"))
	if err == nil {
		c.Close()
		t.Fatal("wrong password was accepted")
	}
	ki := ssh.KeyboardInteractive(func(user, instr string, questions []string, echos []bool) ([]string, error) {
		if len(questions) != 1 || questions[0] != "Password: " || echos[0] {
			t.Errorf("unexpected question %q %v", questions, echos)
		}
		return []string{"secret"}, nil
	})
	c, err = dial(t, s, ki)
	if err != nil {
		t.Fatalf("keyboard-interactive failed: %v", err)
	}
	c.Close()
	bad := ssh.KeyboardInteractive(func(string, string, []string, []bool) ([]string, error) {
		return []string{"nope"}, nil
	})
	c, err = dial(t, s, bad)
	if err == nil {
		c.Close()
		t.Fatal("wrong keyboard-interactive answer was accepted")
	}
	if line := s.KnownHostsLine(); len(line) < 20 || line[:len("[127.0.0.1]:")] != "[127.0.0.1]:" {
		t.Fatalf("KnownHostsLine = %q", line)
	}
}

func TestDropAllAndCount(t *testing.T) {
	s := Start(t, t.TempDir(), Options{User: "alice", Password: "secret"})
	var clients []*ssh.Client
	for i := 0; i < 2; i++ {
		c, err := dial(t, s, ssh.Password("secret"))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		clients = append(clients, c)
	}
	if n := s.Connections(); n != 2 {
		t.Fatalf("Connections() = %d, want 2", n)
	}
	s.DropAll()
	for i, c := range clients {
		done := make(chan error, 1)
		go func() { done <- c.Wait() }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatalf("connection %d still open after DropAll", i)
		}
	}
	if n := s.Connections(); n != 2 {
		t.Fatalf("Connections() after drop = %d, want 2", n)
	}
}
