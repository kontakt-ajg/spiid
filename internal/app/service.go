// Package app is the facade every front end uses: servers, settings, credentials, browsing and downloads.
package app

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	"spiid/internal/config"
	"spiid/internal/engine"
	"spiid/internal/errs"
	"spiid/internal/logx"
	"spiid/internal/sshconn"
)

const version = "0.1.0"

// Prompter asks the user for host-key confirmations and secrets.
type Prompter = sshconn.Prompter

// HostKeyRequest, SecretRequest, SecretKind and SecretAnswer are the prompt types.
type (
	// HostKeyRequest describes the host key awaiting confirmation.
	HostKeyRequest = sshconn.HostKeyRequest
	// SecretRequest describes the secret being requested.
	SecretRequest = sshconn.SecretRequest
	// SecretKind identifies a password, passphrase, or master password.
	SecretKind = sshconn.SecretKind
	// SecretAnswer contains the supplied secret and save preference.
	SecretAnswer = sshconn.SecretAnswer
)

// Options configures New.
type Options struct {
	// Log receives terminal messages; nil is silent.
	Log *logx.Logger
	// ConfigDir overrides the default configuration folder.
	ConfigDir string // "" -> config.DefaultDir(Home)
	// Home overrides the user home used for default paths.
	Home string // "" -> os.UserHomeDir()
	// KnownHosts overrides the SSH known-hosts file path.
	KnownHosts string // "" -> <Home>/.ssh/known_hosts
	// Prompter asks for host-key confirmations and secrets.
	Prompter Prompter
}

// Service is the facade; all methods are safe for concurrent use.
type Service struct {
	log   *logx.Logger
	home  string
	store *config.Store
	ssh   *sshconn.Manager
	eng   *engine.Manager

	ctx    context.Context // canceled by Close; background walks use it
	cancel context.CancelFunc
	walks  sync.WaitGroup
	once   sync.Once

	edit sync.Mutex // serializes server-list changes (they may wait for a prompt)

	mu       sync.Mutex // servers, settings, closed
	servers  []config.Server
	settings config.Settings
	closed   bool
	scans    []*runningScan
	nextScan int64

	msgMu   sync.Mutex // messages
	msgs    []Message
	msgBase int // index of msgs[0]
}

// nilPrompter is used when no Prompter is given: every prompt is canceled.
type nilPrompter struct{}

// ConfirmHostKey cancels host-key requests when no prompter is configured.
func (nilPrompter) ConfirmHostKey(context.Context, HostKeyRequest) (bool, error) {
	return false, errs.New(errs.Canceled, "prompt", "", nil)
}

// Secret cancels secret requests when no prompter is configured.
func (nilPrompter) Secret(context.Context, SecretRequest) (SecretAnswer, error) {
	return SecretAnswer{}, errs.New(errs.Canceled, "prompt", "", nil)
}

// poolSource adapts sshconn.Manager to engine.PoolSource.
type poolSource struct{ m *sshconn.Manager }

// Pool returns the server connection pool used by downloads.
func (p poolSource) Pool(srv config.Server, max int) engine.Pool { return p.m.Pool(srv, max) }

// New opens the config folder, loads servers and settings and starts the managers.
func New(opts Options) (*Service, error) {
	home := opts.Home
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, errs.New(errs.Other, "home", "", err)
		}
		home = h
	}
	dir := opts.ConfigDir
	if dir == "" {
		d, err := config.DefaultDir(home)
		if err != nil {
			return nil, err
		}
		dir = d
	}
	known := opts.KnownHosts
	if known == "" {
		known = filepath.Join(home, ".ssh", "known_hosts")
	}
	prompter := opts.Prompter
	if prompter == nil {
		prompter = nilPrompter{}
	}
	s := &Service{log: opts.Log, home: home}
	var err error
	if s.store, err = config.Open(filepath.Join(dir, "config.json"), opts.Log, home); err != nil {
		opts.Log.Error("config: load failed: %v", err)
		return nil, err
	}
	if s.servers, err = s.store.LoadServers(); err != nil {
		return nil, err
	}
	if s.settings, err = s.store.LoadSettings(); err != nil {
		return nil, err
	}
	so := sshconn.Options{
		Log: opts.Log, Home: home, KnownHosts: known, Credentials: s.store, Prompter: prompter,
		Warn: func(msg string) { s.addMessage(msg) },
	}
	s.ssh = sshconn.NewManager(so)
	st := s.settings
	s.eng = engine.NewManager(engine.Options{
		Log: opts.Log, Pools: poolSource{s.ssh}, ThreadsPerFile: st.ThreadsPerFile, ParallelFiles: st.ParallelFiles,
		MaxConnections: st.MaxConnections,
		OnRetry:        s.ssh.ForgetSecrets,
	})
	s.ctx, s.cancel = context.WithCancel(context.Background())
	return s, nil
}

// Close cancels the background walks and stops the engine and the connections; it is idempotent.
func (s *Service) Close() {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.cancel()
		s.walks.Wait()
		s.eng.Close()
		s.ssh.Close()
	})
}

// ConfigDir is the folder holding config.json.
func (s *Service) ConfigDir() string { return s.store.Dir() }

// Version is the core's version.
func (s *Service) Version() string { return version }
