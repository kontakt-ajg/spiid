package web

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"spiid/internal/app"
	"spiid/internal/errs"
)

func promptEnv() (context.CancelFunc, *prompter, chan map[string]json.RawMessage) {
	c, cancel := context.WithCancel(context.Background())
	ch := make(chan map[string]json.RawMessage, 10)
	return cancel, newPrompter(
		c,
		func(s string) { var e map[string]json.RawMessage; json.Unmarshal([]byte(s), &e); ch <- e },
	), ch
}

func TestPrompterHostKeyAndSecret(t *testing.T) {
	cancel, p, ch := promptEnv()
	defer cancel()
	done := make(chan error, 1)
	go func() {
		ok, e := p.ConfirmHostKey(context.Background(),
			app.HostKeyRequest{Server: "box", Host: "h", Port: 22, KeyType: "ssh-ed25519", Fingerprint: "SHA256:x"})
		if !ok && e == nil {
			e = context.Canceled
		}
		done <- e
	}()
	e := event(t, ch)
	want := `{"type":"prompt","id":1,"prompt":"host_key","server":"box","host":"h","port` +
		`":22,"key_type":"ssh-ed25519","fingerprint":"SHA256:x"}`
	var expected map[string]json.RawMessage
	json.Unmarshal([]byte(want), &expected)
	if !reflect.DeepEqual(e, expected) {
		t.Fatal(marshal(e))
	}
	p.answer(99, `{"accept":false}`)
	p.answer(1, `{"accept":true}`)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	for i, a := range []string{`{"value":"pw","save":true}`, `{"cancel":true}`, `{"value":"pw"}`, "bad"} {
		go func() {
			a, e := p.Secret(context.Background(), app.SecretRequest{Server: "box", Kind: "password", OfferSave: true})
			if i == 0 && (a.Value != "pw" || !a.Save) {
				t.Error(a)
			}
			if i == 2 && (a.Value != "pw" || a.Save) {
				t.Error(a)
			}
			done <- e
		}()
		e := event(t, ch)
		var id int64
		json.Unmarshal(e["id"], &id)
		if len(e) != 8 || string(e["prompt"]) != "\"secret\"" || string(e["retry"]) != "false" ||
			string(e["rejected"]) != "false" || string(e["offer_save"]) != "true" {
			t.Fatal(marshal(e))
		}
		p.answer(id, a)
		err := <-done
		if (i == 0 || i == 2) && err != nil || (i == 1 || i == 3) && errs.KindOf(err) != errs.Canceled {
			t.Fatal(err)
		}
	}
}

func TestPrompterCtxCancel(t *testing.T) {
	stop, p, ch := promptEnv()
	defer stop()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := p.Secret(ctx, app.SecretRequest{}); done <- e }()
	event(t, ch)
	cancel()
	if e := <-done; errs.KindOf(e) != errs.Canceled {
		t.Fatal(e)
	}
	p.answer(1, `{"value":"late","save":false}`)
	p.mu.Lock()
	n := len(p.pending)
	p.mu.Unlock()
	if n != 0 {
		t.Fatal(n)
	}
	select {
	case e := <-ch:
		t.Fatal(e)
	case <-time.After(time.Millisecond):
	}
}

func TestPromptDeadlineCancelsAndClearsSnapshot(t *testing.T) {
	stop, p, ch := promptEnv()
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := p.Secret(ctx, app.SecretRequest{}); done <- err }()
	event(t, ch)
	p.mu.Lock()
	pending := len(p.events)
	p.mu.Unlock()
	if pending != 1 {
		t.Fatal("prompt was not pending before deadline")
	}
	if err := <-done; errs.KindOf(err) != errs.Canceled {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.events) != 0 || len(p.pending) != 0 {
		t.Fatal("expired prompt retained")
	}
}
