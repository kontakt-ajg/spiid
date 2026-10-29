package web

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"spiid/internal/app"
	"spiid/internal/config"
	"spiid/internal/errs"
	"spiid/internal/sftptest"
)

func downloadEnv(t *testing.T) (*Server, chan map[string]json.RawMessage, []byte, string) {
	t.Helper()
	root := t.TempDir()
	data := bytes.Repeat([]byte("download"), 8192)
	if e := os.WriteFile(filepath.Join(root, "file"), data, 0600); e != nil {
		t.Fatal(e)
	}
	ss := sftptest.Start(t, root, sftptest.Options{User: "user", Password: "pw"})
	b, ch := testMethods(t)
	srv := config.Server{Name: "box", Host: ss.Host(), Port: ss.Port(), User: "user", Auth: config.AuthPassword}
	call(t, b, ch, "servers.add", map[string]any{"server": srv, "secret": ""})
	return b, ch, data, filepath.Join(b.service.Settings().DownloadDir, "file")
}

func promptAnswer(t *testing.T, b *Server, ch chan map[string]json.RawMessage, kind string) {
	t.Helper()
	e := event(t, ch)
	if string(e["type"]) != "\"prompt\"" || string(e["prompt"]) != marshal(kind) {
		t.Fatalf("prompt %s", marshal(e))
	}
	var id int64
	json.Unmarshal(e["id"], &id)
	if kind == "host_key" {
		b.prompts.answer(id, `{"accept":true}`)
	} else {
		b.prompts.answer(id, `{"value":"pw","save":false}`)
	}
}

func startDownload(t *testing.T, b *Server, ch chan map[string]json.RawMessage) {
	t.Helper()
	call(t, b, ch, "downloads.add", map[string]any{"server": "box", "paths": []string{"/file"}, "overwrite": false})
	promptAnswer(t, b, ch, "host_key")
}

func TestE2EDownloadThroughMethods(t *testing.T) {
	b, ch, want, path := downloadEnv(t)
	startDownload(t, b, ch)
	promptAnswer(t, b, ch, "secret")
	deadline := time.Now().Add(10 * time.Second)
	for {
		var jobs struct {
			Jobs []map[string]json.RawMessage `json:"jobs"`
		}
		json.Unmarshal(call(t, b, ch, "downloads.state", sinceParams{ptr(0)}), &jobs)
		if len(jobs.Jobs) == 1 && string(jobs.Jobs[0]["state"]) == "\"done\"" {
			j := jobs.Jobs[0]
			keys := []string{
				"id",
				"server",
				"remote",
				"local",
				"state",
				"size",
				"done",
				"speed",
				"segments",
				"active_segments",
				"error",
				"error_kind",
				"added",
			}
			if len(j) != len(keys) {
				t.Fatal(marshal(j))
			}
			for _, k := range keys {
				if _, ok := j[k]; !ok {
					t.Fatal(k)
				}
			}
			if string(j["error_kind"]) != "\"\"" {
				t.Fatal(marshal(j))
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(marshal(jobs))
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(got, want) {
		t.Fatalf("download mismatch: %v", e)
	}
	listing := call(t, b, ch, "browse.list", browseParams{ptr("box"), ptr("/")})
	var l struct {
		Entries []map[string]json.RawMessage `json:"entries"`
	}
	json.Unmarshal(listing, &l)
	if len(l.Entries) != 1 || len(l.Entries[0]) != 6 {
		t.Fatal(string(listing))
	}
	for _, k := range []string{"name", "path", "is_dir", "is_link", "size", "mtime"} {
		if _, ok := l.Entries[0][k]; !ok {
			t.Fatal(k)
		}
	}
}

func TestStopCancelsPrompts(t *testing.T) {
	b, ch := testMethods(t)
	done := make(chan error, 1)
	go func() {
		_, err := b.prompts.Secret(context.Background(), app.SecretRequest{})
		done <- err
	}()
	e := event(t, ch)
	if string(e["prompt"]) != "\"secret\"" {
		t.Fatal(marshal(e))
	}
	b.prompts.mu.Lock()
	n := len(b.prompts.pending)
	b.prompts.mu.Unlock()
	if n != 1 {
		t.Fatalf("not waiting: %d", n)
	}
	b.Close()
	select {
	case err := <-done:
		if errs.KindOf(err) != errs.Canceled {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Secret did not return after stop")
	}
	b.prompts.mu.Lock()
	n = len(b.prompts.pending)
	b.prompts.mu.Unlock()
	if n != 0 {
		t.Fatalf("pending prompts after stop: %d", n)
	}
	b.prompts.answer(1, `{"value":"late","save":false}`)
	select {
	case e := <-ch:
		t.Fatalf("event after stop: %s", marshal(e))
	case <-time.After(20 * time.Millisecond):
	}
	_, err := b.callMethod(context.Background(), "servers.list", json.RawMessage("{}"))
	if errs.KindOf(err) != errs.Canceled {
		t.Fatal(err)
	}
}
