package web

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"spiid/internal/app"
	"spiid/internal/errs"
	"spiid/internal/logx"
)

type prompter struct {
	log     *logx.Logger
	ctx     context.Context
	emit    func(string)
	mu      sync.Mutex
	next    int64
	pending map[int64]chan string
	stopped bool
	events  map[int64]string
}

func newPrompter(ctx context.Context, emit func(string)) *prompter {
	return &prompter{ctx: ctx, emit: emit, pending: make(map[int64]chan string), events: make(map[int64]string)}
}

func (p *prompter) wait(ctx context.Context, fields map[string]any) (answer string, err error) {
	label := "master password"
	if fields["prompt"] == "host_key" {
		label = fmt.Sprintf("host key for %s", fields["server"])
	} else if fields["server"] != "" {
		label = fmt.Sprintf("%s for %s", fields["kind"], fields["server"])
	}
	p.log.Info("waiting for your answer in the browser: %s", label)
	defer func() {
		var canceledAnswer struct {
			// Cancel cancels the prompt.
			Cancel bool `json:"cancel"`
		}
		_ = json.Unmarshal([]byte(answer), &canceledAnswer)
		if err != nil || canceledAnswer.Cancel {
			p.log.Info("browser prompt canceled: %s", label)
		} else {
			p.log.Info("browser prompt answered: %s", label)
		}
	}()
	p.mu.Lock()
	if p.stopped || p.ctx.Err() != nil || ctx.Err() != nil {
		p.mu.Unlock()
		return "", canceled()
	}
	p.next++
	id := p.next
	ch := make(chan string, 1)
	p.pending[id] = ch
	fields["type"] = "prompt"
	fields["id"] = id
	p.events[id] = marshal(fields)
	p.emit(p.events[id])
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, id); delete(p.events, id); p.mu.Unlock() }()
	select {
	case <-ctx.Done():
		return "", canceled()
	case <-p.ctx.Done():
		return "", canceled()
	case s := <-ch:
		if ctx.Err() != nil || p.ctx.Err() != nil {
			return "", canceled()
		}
		return s, nil
	}
}

func (p *prompter) answer(id int64, answer string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ch, ok := p.pending[id]; ok {
		delete(p.pending, id)
		delete(p.events, id)
		ch <- answer
	}
}

func (p *prompter) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopped = true
	for id, ch := range p.pending {
		ch <- `{"cancel":true}`
		delete(p.pending, id)
		delete(p.events, id)
	}
}

// ConfirmHostKey asks the browser to confirm the remote host key.
func (p *prompter) ConfirmHostKey(ctx context.Context, r app.HostKeyRequest) (bool, error) {
	fields := map[string]any{
		"prompt":      "host_key",
		"server":      r.Server,
		"host":        r.Host,
		"port":        r.Port,
		"key_type":    r.KeyType,
		"fingerprint": r.Fingerprint,
	}
	s, err := p.wait(ctx, fields)
	if err != nil {
		return false, err
	}
	var a struct {
		// Accept confirms that the host key should be trusted.
		Accept *bool `json:"accept"`
	}
	if decode([]byte(s), &a) != nil || a.Accept == nil {
		return false, canceled()
	}
	return *a.Accept, nil
}

// Secret asks the browser for a password or key passphrase.
func (p *prompter) Secret(ctx context.Context, r app.SecretRequest) (app.SecretAnswer, error) {
	fields := map[string]any{
		"prompt":     "secret",
		"kind":       r.Kind,
		"server":     r.Server,
		"retry":      r.Retry,
		"rejected":   r.Rejected,
		"offer_save": r.OfferSave,
	}
	s, err := p.wait(ctx, fields)
	if err != nil {
		return app.SecretAnswer{}, err
	}
	var cancel struct {
		// Cancel cancels the prompt.
		Cancel bool `json:"cancel"`
	}
	if json.Unmarshal([]byte(s), &cancel) != nil || cancel.Cancel {
		return app.SecretAnswer{}, canceled()
	}
	var a struct {
		// Value contains the supplied secret.
		Value *string `json:"value"`
		// Save requests saving the secret.
		Save bool `json:"save"`
	}
	if decode([]byte(s), &a) != nil || a.Value == nil {
		return app.SecretAnswer{}, canceled()
	}
	return app.SecretAnswer{Value: *a.Value, Save: a.Save}, nil
}

func canceled() error { return errs.New(errs.Canceled, "prompt", "", nil) }

func (s *Server) snapshotPrompts(register func()) []string {
	p := s.prompts
	p.mu.Lock()
	defer p.mu.Unlock()
	if register != nil {
		register()
	}
	out := make([]string, 0, len(p.events))
	for _, event := range p.events {
		out = append(out, event)
	}
	return out
}
