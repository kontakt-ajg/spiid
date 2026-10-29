package sshconn

import (
	"context"

	"spiid/internal/errs"
)

// HostKeyRequest describes a host key the user must confirm.
type HostKeyRequest struct {
	Server      string // server name
	Host        string
	Port        int
	KeyType     string // e.g. "ssh-ed25519"
	Fingerprint string // "SHA256:…"
}

// SecretKind says what a SecretRequest asks for.
type SecretKind string

// The kinds of secret the user can be asked for.
const (
	SecretPassword   SecretKind = "password"
	SecretPassphrase SecretKind = "passphrase"
	SecretMaster     SecretKind = "master"
)

// SecretRequest asks the user for a secret.
type SecretRequest struct {
	Server    string // empty for SecretMaster
	Kind      SecretKind
	Retry     bool // the previous typed value was wrong
	Rejected  bool // the saved value was rejected; ask for a replacement
	OfferSave bool // show a "Save password/passphrase" box (checked by default)
}

// SecretAnswer is the user's reply to a SecretRequest.
type SecretAnswer struct {
	Value string
	Save  bool // only meaningful when OfferSave; saved after the login succeeds
}

// Prompter asks the user. Implementations block until answered; a cancel by the user returns errs.Canceled.
// sshconn calls it from background goroutines, never two calls at once.
type Prompter interface {
	ConfirmHostKey(ctx context.Context, req HostKeyRequest) (bool, error)
	Secret(ctx context.Context, req SecretRequest) (SecretAnswer, error)
}

func secretKey(server string, kind SecretKind) string {
	return server + "\x00" + string(kind)
}

// acquire takes the prompt lock; it gives up when ctx is canceled.
func (m *Manager) acquire(ctx context.Context) error {
	select {
	case m.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return errs.New(errs.Canceled, "prompt", "", ctx.Err())
	}
}

func (m *Manager) release() {
	<-m.sem
}

func (m *Manager) cached(server string, kind SecretKind) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.secrets[secretKey(server, kind)]
	return v, ok
}

func (m *Manager) cache(server string, kind SecretKind, value string) {
	m.mu.Lock()
	m.secrets[secretKey(server, kind)] = value
	m.mu.Unlock()
}

// promptError returns errs.Canceled errors as they are and wraps every other prompter error as errs.Other.
func promptError(err error) error {
	if errs.KindOf(err) == errs.Canceled {
		return err
	}
	return errs.New(errs.Other, "prompt", "", err)
}

// callSecret calls the Prompter; the caller holds the prompt lock.
func (m *Manager) callSecret(ctx context.Context, req SecretRequest) (SecretAnswer, error) {
	ans, err := m.opts.Prompter.Secret(ctx, req)
	if err != nil {
		return SecretAnswer{}, promptError(err)
	}
	return ans, nil
}

// ask prompts for a secret of this login: it pauses the dial deadline, takes the prompt lock and, when recheck
// looks in the session cache again before prompting: a cached value other than tried (the candidate this login
// just tried) is used without prompting. A typed value goes into the session cache.
func (l *login) ask(req SecretRequest, tried string) (SecretAnswer, error) {
	l.gate.pause()
	defer l.gate.resume()
	if err := l.m.acquire(l.ctx); err != nil {
		return SecretAnswer{}, l.fail(err)
	}
	defer l.m.release()
	if v, ok := l.m.cached(req.Server, req.Kind); ok && v != tried {
		return SecretAnswer{Value: v}, nil
	}
	ans, err := l.m.callSecret(l.ctx, req)
	if err != nil {
		return SecretAnswer{}, l.fail(err)
	}
	l.m.cache(req.Server, req.Kind, ans.Value)
	return ans, nil
}
