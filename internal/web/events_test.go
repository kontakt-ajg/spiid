package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"spiid/internal/config"
	"spiid/internal/sftptest"
)

func connect(t *testing.T, s *Server) (*http.Response, *bufio.Reader) {
	t.Helper()
	req, _ := http.NewRequest("GET", "http://"+s.host+"/api/events?t="+s.token, nil)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != 200 {
		t.Fatal(resp.Status)
	}
	return resp, bufio.NewReader(resp.Body)
}

func nextPrompt(t *testing.T, r *bufio.Reader) map[string]json.RawMessage {
	t.Helper()
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "data: ") {
			var p map[string]json.RawMessage
			if err = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &p); err != nil {
				t.Fatal(err)
			}
			return p
		}
	}
}

func answerPrompt(t *testing.T, s *Server, p map[string]json.RawMessage, answer string) {
	t.Helper()
	r := post(t, s, "/api/answer", `{"id":`+string(p["id"])+`,"answer":`+answer+`}`)
	if r.StatusCode != 204 {
		t.Fatal(r.Status)
	}
}

func TestEventsPromptAndReplay(t *testing.T) {
	s, b := setup(t, nil)
	remote := sftptest.Start(t, t.TempDir(), sftptest.Options{User: "u", Password: "pw"})
	srv := config.Server{
		Name: "box",
		Host: remote.Host(),
		Port: remote.Port(),
		User: "u",
		Auth: config.AuthPassword,
	}
	if err := b.Service().AddServer(context.Background(), srv, ""); err != nil {
		t.Fatal(err)
	}
	resp, r := connect(t, s)
	done := make(chan error, 1)
	go func() { _, err := b.Service().List(context.Background(), "box", "/"); done <- err }()
	host := nextPrompt(t, r)
	if string(host["prompt"]) != `"host_key"` {
		t.Fatal(host)
	}
	answerPrompt(t, s, host, `{"accept":true}`)
	password := nextPrompt(t, r)
	if string(password["prompt"]) != `"secret"` || string(password["kind"]) != `"password"` {
		t.Fatal(password)
	}
	if len(b.snapshotPrompts(nil)) != 1 {
		t.Fatal("password is not pending")
	}
	later, r2 := connect(t, s)
	replay := nextPrompt(t, r2)
	if string(replay["id"]) != string(password["id"]) {
		t.Fatal(replay)
	}
	answerPrompt(t, s, password, `{"value":"pw","save":false}`)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("answer did not resolve prompt")
	}
	resp.Body.Close()
	later.Body.Close()
}

func TestSlowStreamDropped(t *testing.T) {
	s, _ := setup(t, nil)
	st := &stream{events: make(chan string, 64), done: make(chan struct{})}
	s.mu.Lock()
	s.streams[st] = true
	s.mu.Unlock()
	for i := 0; i < 65; i++ {
		s.broadcast(marshal(map[string]any{"type": "prompt", "id": i}))
	}
	select {
	case <-st.done:
	case <-time.After(2 * time.Second):
		t.Fatal("full stream blocked broadcast")
	}
	s.mu.Lock()
	_, exists := s.streams[st]
	s.mu.Unlock()
	if exists {
		t.Fatal("stream still registered")
	}
}
