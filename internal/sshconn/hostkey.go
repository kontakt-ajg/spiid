package sshconn

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"spiid/internal/errs"
)

// checkKnown reads known_hosts afresh and checks key; a missing file means no known hosts.
func (m *Manager) checkKnown(hostname string, remote net.Addr, key ssh.PublicKey) error {
	cb, err := knownhosts.New(m.opts.KnownHosts)
	if errors.Is(err, fs.ErrNotExist) {
		return &knownhosts.KeyError{}
	}
	if err != nil {
		return errs.New(errs.Other, "known_hosts", "", err)
	}
	return cb(hostname, remote, key)
}

// hostKeyCallback verifies a server's host key, asking the user on first contact.
func (l *login) hostKeyCallback(hostname string, remote net.Addr, key ssh.PublicKey) error {
	err := l.m.checkKnown(hostname, remote, key)
	if err == nil {
		return nil
	}
	if e := l.hostKeyProblem(err); e != nil {
		return l.fail(e)
	}
	return l.fail(l.firstContact(hostname, remote, key))
}

// hostKeyProblem returns the error to report for a known_hosts result, or nil for an unknown host.
func (l *login) hostKeyProblem(err error) error {
	var ke *knownhosts.KeyError
	var re *knownhosts.RevokedError
	if errors.As(err, &re) || (errors.As(err, &ke) && len(ke.Want) > 0) {
		l.m.opts.Log.Warn("ssh: host key mismatch for %s", l.srv.Name)
		return errs.New(errs.HostKeyMismatch, "host key", l.srv.Name, err)
	}
	if !errors.As(err, &ke) {
		return err
	}
	return nil
}

func (l *login) firstContact(hostname string, remote net.Addr, key ssh.PublicKey) error {
	fp := ssh.FingerprintSHA256(key)
	rk := hostname + " " + fp
	rejected := errs.New(errs.HostKeyRejected, "host key", l.srv.Name, nil)
	if l.m.isRejected(rk) {
		return rejected
	}
	l.gate.pause()
	defer l.gate.resume()
	if err := l.m.acquire(l.ctx); err != nil {
		return err
	}
	defer l.m.release()
	err := l.m.checkKnown(hostname, remote, key)
	if err == nil {
		return nil
	}
	if e := l.hostKeyProblem(err); e != nil {
		return e
	}
	if l.m.isRejected(rk) {
		return rejected
	}
	req := HostKeyRequest{
		Server:      l.srv.Name,
		Host:        l.srv.Host,
		Port:        l.srv.Port,
		KeyType:     key.Type(),
		Fingerprint: fp,
	}
	ok, err := l.m.opts.Prompter.ConfirmHostKey(l.ctx, req)
	if err != nil {
		return promptError(err)
	}
	if !ok {
		l.m.opts.Log.Warn("ssh: host key rejected for %s", l.srv.Name)
		l.m.reject(rk)
		return rejected
	}
	l.m.opts.Log.Warn("ssh: host key accepted for %s", l.srv.Name)
	if err := l.m.appendKnown(hostname, key); err != nil {
		return errs.New(errs.Other, "known_hosts", "", err)
	}
	return nil
}

func (m *Manager) isRejected(rk string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.rejected[rk]
	return ok
}

func (m *Manager) reject(rk string) {
	m.mu.Lock()
	m.rejected[rk] = struct{}{}
	m.mu.Unlock()
}

func (m *Manager) appendKnown(hostname string, key ssh.PublicKey) error {
	if err := os.MkdirAll(filepath.Dir(m.opts.KnownHosts), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(m.opts.KnownHosts, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key) + "\n"
	if fi, err := f.Stat(); err == nil && fi.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, fi.Size()-1); err == nil && last[0] != '\n' {
			line = "\n" + line
		}
	}
	if _, err := f.WriteString(line); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// knownKeyTypes returns the key types known_hosts holds for hostname.
func (m *Manager) knownKeyTypes(hostname string, remote net.Addr) []string {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil
	}
	probe, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil
	}
	var ke *knownhosts.KeyError
	if !errors.As(m.checkKnown(hostname, remote, probe), &ke) {
		return nil
	}
	var types []string
	for _, w := range ke.Want {
		types = append(types, w.Key.Type())
	}
	return types
}

// hostKeyAlgorithms lists the algorithms of the known key types first, then the rest of the supported ones.
func hostKeyAlgorithms(knownTypes []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(a string) {
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	for _, t := range knownTypes {
		switch {
		case strings.HasSuffix(t, "-cert-v01@openssh.com"):
		case t == ssh.KeyAlgoRSA:
			add(ssh.KeyAlgoRSASHA512)
			add(ssh.KeyAlgoRSASHA256)
		default:
			add(t)
		}
	}
	for _, a := range ssh.SupportedAlgorithms().HostKeys {
		add(a)
	}
	return out
}
