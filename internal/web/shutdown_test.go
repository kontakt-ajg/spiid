package web

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"spiid/internal/app"
	"spiid/internal/config"
	"spiid/internal/errs"
)

func TestCloseCancelsPromptAndEndsStreams(t *testing.T) {
	s, _ := setup(t, nil)
	if err := s.Service().AddServer(context.Background(), config.Server{
		Name: "box", Host: "127.0.0.1", User: "u", Auth: config.AuthAgent,
	}, ""); err != nil {
		t.Fatal(err)
	}
	resp, reader := connect(t, s)
	defer resp.Body.Close()
	promptDone := make(chan error, 1)
	go func() {
		_, err := s.prompts.Secret(context.Background(), app.SecretRequest{})
		promptDone <- err
	}()
	nextPrompt(t, reader)
	if len(s.snapshotPrompts(nil)) != 1 {
		t.Fatal("prompt is not pending")
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); s.Close() }()
	}
	closed := make(chan struct{})
	go func() { wg.Wait(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close waited for the prompt or stream")
	}
	if err := <-promptDone; errs.KindOf(err) != errs.Canceled {
		t.Fatal(err)
	}
	if len(s.snapshotPrompts(nil)) != 0 {
		t.Fatal("prompt retained after Close")
	}
	select {
	case <-s.Done():
	default:
		t.Fatal("Done is still open")
	}
	if err := s.Service().Download("box", []string{"/"}, false); errs.KindOf(err) != errs.Canceled {
		t.Fatal("closed service accepted download")
	}
}

func TestListenerFailureClosesServer(t *testing.T) {
	s, _ := setup(t, nil)
	if err := s.listener.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("listener failure did not stop server")
	}
}

func TestInvalidQuitParamsLeaveServerRunning(t *testing.T) {
	s, _ := setup(t, nil)
	r := post(t, s, "/api/call", `{"method":"app.quit","params":[]}`)
	var result map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result["error"] == nil {
		t.Fatal("invalid quit parameters accepted")
	}
	select {
	case <-s.Done():
		t.Fatal("invalid quit stopped server")
	default:
	}
}

func TestRemovedLauncherRoutesRequireTokenAndReturnNotFound(t *testing.T) {
	s, _ := setup(t, nil)
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/ping"},
		{"POST", "/api/quit"},
	} {
		r := request(t, s, route.method, route.path, "{}", true, map[string]string{
			"Origin": "http://" + s.host, "Content-Type": "application/json",
		})
		if r.StatusCode != 404 {
			t.Fatalf("%s: %s", route.path, r.Status)
		}
	}
	r := request(t, s, "GET", "/api/ping", "", false, map[string]string{"X-Spiid-Token": s.token})
	if r.StatusCode != 401 {
		t.Fatal("launcher header authenticated request:", r.Status)
	}
}
