package web

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"spiid/internal/app"
	"spiid/internal/config"
)

func testMethods(t *testing.T) (*Server, chan map[string]json.RawMessage) {
	t.Helper()
	events := make(chan map[string]json.RawMessage, 256)
	home := t.TempDir()
	b, e := Start(Options{App: app.Options{Home: home, ConfigDir: filepath.Join(home, "config"),
		KnownHosts: filepath.Join(home, "known_hosts")}})
	if e != nil {
		t.Fatal(e)
	}
	st := &stream{events: make(chan string, 256), done: make(chan struct{})}
	b.mu.Lock()
	b.streams[st] = true
	b.mu.Unlock()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case raw := <-st.events:
				var v map[string]json.RawMessage
				if err := json.Unmarshal([]byte(raw), &v); err != nil {
					panic(err)
				}
				events <- v
			case <-st.done:
				return
			}
		}
	}()
	t.Cleanup(func() { b.Close(); <-stopped })
	return b, events
}

func event(t *testing.T, ch chan map[string]json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(10 * time.Second):
		t.Fatal("event timeout")
		return nil
	}
}

func call(t *testing.T, b *Server, ch chan map[string]json.RawMessage, m string, p any) json.RawMessage {
	t.Helper()
	result, err := b.callMethod(context.Background(), m, json.RawMessage(marshal(p)))
	if err != nil {
		t.Fatal(err)
	}
	return json.RawMessage(marshal(result))
}

func TestMethodsUnknownMethod(t *testing.T) {
	b, _ := testMethods(t)
	_, err := b.callMethod(context.Background(), "unknown", json.RawMessage("{}"))
	if errorJSON(err).Kind != "invalid_input" {
		t.Fatal(err)
	}
}

func TestMethodsBadParams(t *testing.T) {
	b, _ := testMethods(t)
	for _, p := range []string{
		"{", "{}", "null", "[]", `{"name":null}`, `{"name":3}`,
		`{"name":"x"}{}`, `{"name":"x","extra":true}`,
	} {
		_, err := b.callMethod(context.Background(), "servers.remove", json.RawMessage(p))
		if err == nil || errorJSON(err).Kind != "invalid_input" {
			t.Fatalf("%s: %v", p, err)
		}
	}
}

func TestMethodsOneResultPerCall(t *testing.T) {
	b, ch := testMethods(t)
	for range 50 {
		call(t, b, ch, "servers.list", empty{})
	}
	select {
	case e := <-ch:
		t.Fatalf("unexpected event: %v", e)
	default:
	}
}

func TestMethodsServersRoundTrip(t *testing.T) {
	b, ch := testMethods(t)
	srv := config.Server{Name: "box", Host: "h", User: "u", Auth: config.AuthPassword}
	call(t, b, ch, "servers.add", map[string]any{"server": srv, "secret": "secret"})
	var list struct {
		Servers []config.Server `json:"servers"`
	}
	json.Unmarshal(call(t, b, ch, "servers.list", empty{}), &list)
	if len(list.Servers) != 1 || list.Servers[0].Name != "box" {
		t.Fatal(list)
	}
	srv.Name = "renamed"
	call(t, b, ch, "servers.update", map[string]any{"old_name": "box", "server": srv, "secret": nil})
	store, e := config.Open(filepath.Join(b.service.ConfigDir(), "config.json"), nil)
	if e != nil {
		t.Fatal(e)
	}
	v, ok, e := store.Get("renamed", config.Password)
	if e != nil || !ok || v != "secret" {
		t.Fatalf("%q %v %v", v, ok, e)
	}
	call(t, b, ch, "servers.remove", nameParams{ptr("renamed")})
	json.Unmarshal(call(t, b, ch, "servers.list", empty{}), &list)
	if len(list.Servers) != 0 {
		t.Fatal(list)
	}
}

func TestMethodsSettingsNormalized(t *testing.T) {
	b, ch := testMethods(t)
	s := b.service.Settings()
	s.ThreadsPerFile = 99
	var got config.Settings
	json.Unmarshal(call(t, b, ch, "settings.save", s), &got)
	if got.ThreadsPerFile != 32 {
		t.Fatal(got)
	}
}
