package sshconn

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"spiid/internal/config"
	"spiid/internal/errs"
	"spiid/internal/fsutil"
)

// login is the state of one dial: who we log in to, the pause gate, the first callback error, a pending save.
type login struct {
	m            *Manager
	ctx          context.Context
	srv          config.Server
	gate         *gate
	agentC       interface{ Close() error }
	agentStalled atomic.Bool
	stopAgent    func()

	mu    sync.Mutex
	err   error
	saveK config.Kind
	saveV string
	save  bool
}

// fail remembers the first error a callback produced (the ssh library may wrap or mask it) and returns err.
func (l *login) fail(err error) error {
	if err == nil {
		return nil
	}
	l.mu.Lock()
	if l.err == nil {
		l.err = err
	}
	l.mu.Unlock()
	return err
}

func (l *login) firstErr() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

func (l *login) setSave(kind config.Kind, ans SecretAnswer) {
	l.mu.Lock()
	l.save = ans.Save
	l.saveK = kind
	l.saveV = ans.Value
	l.mu.Unlock()
}

// commit stores the typed secret the user asked to save; called after the login worked.
func (l *login) commit() {
	l.mu.Lock()
	save, kind, value := l.save, l.saveK, l.saveV
	l.mu.Unlock()
	cs := l.m.opts.Credentials
	if !save || cs == nil {
		return
	}
	err := l.m.unlock(l.ctx, nil)
	if err == nil {
		err = cs.Set(l.srv.Name, kind, value)
	}
	if err != nil && l.m.opts.Warn != nil {
		l.m.opts.Warn(fmt.Sprintf("could not save the %s for %s: %v", kind, l.srv.Name, err))
	}
}

func (l *login) cleanup() {
	if l.stopAgent != nil {
		l.stopAgent()
		l.stopAgent = nil
	}
	if l.agentC != nil {
		l.agentC.Close()
		l.agentC = nil
	}
}

// stored reads a saved secret; if it is encrypted and locked, it asks for the master password first.
func (l *login) stored(kind config.Kind) (string, bool, error) {
	cs := l.m.opts.Credentials
	if cs == nil {
		return "", false, nil
	}
	v, ok, err := cs.Get(l.srv.Name, kind)
	if errs.KindOf(err) == errs.Locked {
		if err = l.m.unlock(l.ctx, l.gate); err != nil {
			return "", false, l.fail(err)
		}
		v, ok, err = cs.Get(l.srv.Name, kind)
	}
	if err != nil {
		return "", false, l.fail(err)
	}
	return v, ok, nil
}

// authMethods builds the auth methods of the server; it may prompt (key passphrase) but does not dial.
func (l *login) authMethods() ([]ssh.AuthMethod, error) {
	switch l.srv.Auth {
	case config.AuthAgent:
		return l.agentMethods()
	case config.AuthKey:
		return l.keyMethods()
	case config.AuthPassword:
		src := &pwSource{l: l}
		f := ssh.PasswordCallback(src.next)
		g := ssh.KeyboardInteractive(src.answer)
		return []ssh.AuthMethod{ssh.RetryableAuthMethod(f, 3), ssh.RetryableAuthMethod(g, 3)}, nil
	}
	return nil, errs.New(errs.InvalidInput, "auth", l.srv.Name, fmt.Errorf("unknown auth method %q", l.srv.Auth))
}

func (l *login) agentMethods() ([]ssh.AuthMethod, error) {
	fail := func(err error) error {
		return errs.New(errs.AuthFailed, "agent", l.srv.Name, fmt.Errorf("ssh-agent not available: %w", err))
	}
	if l.m.opts.AgentConn == nil {
		return nil, fail(errors.New("no agent configured"))
	}
	conn, err := l.m.opts.AgentConn()
	if err != nil {
		return nil, fail(err)
	}
	l.agentC = conn
	stopCtx := context.AfterFunc(l.ctx, func() { conn.Close() })
	var timer *time.Timer
	l.stopAgent = func() {
		stopCtx()
		if timer != nil {
			timer.Stop()
		}
	}
	return []ssh.AuthMethod{ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
		if timer == nil {
			timer = time.AfterFunc(l.m.opts.DialTimeout, func() {
				l.agentStalled.Store(true)
				conn.Close()
			})
		}
		return agent.NewClient(conn).Signers()
	})}, nil
}

func (l *login) keyMethods() ([]ssh.AuthMethod, error) {
	path := fsutil.ExpandHome(l.srv.KeyFile, l.m.opts.Home)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errs.New(errs.AuthFailed, "key", l.srv.Name, fmt.Errorf("can't read key file %s: %w", path, err))
	}
	signer, err := ssh.ParsePrivateKey(data)
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		signer, err = l.decryptKey(data)
	}
	if err != nil {
		var ee *errs.Error
		if errors.As(err, &ee) {
			return nil, err
		}
		return nil, errs.New(errs.AuthFailed, "key", l.srv.Name, fmt.Errorf("can't use key file %s: %w", path, err))
	}
	return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
}

// decryptKey finds the passphrase: session cache, credentials, then up to 3 typed tries.
func (l *login) decryptKey(data []byte) (ssh.Signer, error) {
	name := l.srv.Name
	var pass string
	have, fromStore := false, false
	if v, ok := l.m.cached(name, SecretPassphrase); ok {
		pass, have = v, true
	} else if v, ok, err := l.stored(config.Passphrase); err != nil {
		return nil, err
	} else if ok {
		pass, have, fromStore = v, true, true
	}
	rejected, wrong := false, false // the saved / a typed or cached candidate was wrong
	if !have {
		pass = ""
	}
	for typed := 0; typed < 3; {
		if !have {
			req := SecretRequest{
				Server:    name,
				Kind:      SecretPassphrase,
				OfferSave: true,
				Rejected:  rejected,
				Retry:     wrong,
			}
			ans, err := l.ask(req, pass)
			if err != nil {
				return nil, err
			}
			pass = ans.Value
			fromStore = false
			typed++
			l.setSave(config.Passphrase, ans)
		}
		signer, err := ssh.ParsePrivateKeyWithPassphrase(data, []byte(pass))
		if err == nil {
			return signer, nil
		}
		if !errors.Is(err, x509.IncorrectPasswordError) {
			return nil, err
		}
		have = false
		rejected, wrong = fromStore, !fromStore
	}
	return nil, errs.New(errs.AuthFailed, "key", name, errors.New("wrong passphrase"))
}

// pwSource hands out password candidates, one per call of the password / keyboard-interactive callbacks.
type pwSource struct {
	l          *login
	n          int
	prevStored bool
	last       string // the candidate handed out last
}

func (p *pwSource) next() (string, error) {
	l := p.l
	req := SecretRequest{Server: l.srv.Name, Kind: SecretPassword, OfferSave: true}
	first := p.n == 0
	if first {
		if v, ok := l.m.cached(l.srv.Name, SecretPassword); ok {
			p.n++
			p.prevStored = false
			p.last = v
			return v, nil
		}
		v, ok, err := l.stored(config.Password)
		if err != nil {
			return "", err
		}
		if ok {
			p.n++
			p.prevStored = true
			p.last = v
			return v, nil
		}
	} else {
		req.Rejected = p.prevStored
		req.Retry = !p.prevStored
	}
	ans, err := l.ask(req, p.last)
	if err != nil {
		return "", err
	}
	p.n++
	p.prevStored = false
	p.last = ans.Value
	l.setSave(config.Password, ans)
	return ans.Value, nil
}

func (p *pwSource) answer(user, instruction string, questions []string, echos []bool) ([]string, error) {
	if len(questions) == 0 {
		return nil, nil
	}
	pw, err := p.next()
	if err != nil {
		return nil, err
	}
	answers := make([]string, len(questions))
	for i := range answers {
		answers[i] = pw
	}
	return answers, nil
}
