// Package sshconn dials SSH/SFTP connections: host keys, authentication, prompts, keepalive and pools.
package sshconn

import (
	"context"
	"io"
	"net"
	"sync"
	"time"

	"spiid/internal/config"
	"spiid/internal/errs"
	"spiid/internal/logx"
)

// Options configures a Manager.
type Options struct {
	Log           *logx.Logger
	Home          string                                                            // for "~/" in key paths
	KnownHosts    string                                                            // path to known_hosts
	Credentials   *config.Store                                                     // saved secrets; may be nil
	Prompter      Prompter                                                          // asks the user
	DialTimeout   time.Duration                                                     // 0 → 15 s
	Keepalive     time.Duration                                                     // 0 → 5 s
	KeepaliveMiss int                                                               // 0 → 3
	DialContext   func(ctx context.Context, network, addr string) (net.Conn, error) // nil → net.Dialer
	// AgentConn defaults to SSH_AUTH_SOCK or the Windows agent pipe.
	AgentConn func() (io.ReadWriteCloser, error)
	// Warn reports non-fatal problems, such as a failed password save; it may be nil.
	Warn func(msg string)
}

// Manager owns the pools, the session secrets and the prompt lock.
type Manager struct {
	opts Options
	sem  chan struct{} // prompt lock: at most one Prompter call at a time

	mu       sync.Mutex
	pools    map[string]*Pool
	secrets  map[string]string
	rejected map[string]struct{}
	closed   bool
}

// NewManager creates a Manager; zero Options fields take their defaults.
func NewManager(opts Options) *Manager {
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = 15 * time.Second
	}
	if opts.Keepalive <= 0 {
		opts.Keepalive = 5 * time.Second
	}
	if opts.KeepaliveMiss <= 0 {
		opts.KeepaliveMiss = 3
	}
	if opts.DialContext == nil {
		d := &net.Dialer{}
		opts.DialContext = d.DialContext
	}
	if opts.AgentConn == nil {
		opts.AgentConn = defaultAgentConn
	}
	return &Manager{
		opts:     opts,
		sem:      make(chan struct{}, 1),
		pools:    map[string]*Pool{},
		secrets:  map[string]string{},
		rejected: map[string]struct{}{},
	}
}

// Pool returns the pool of srv (one per server name); a changed limit is applied to the existing pool; a changed
// connection field closes the old pool and creates a new one.
func (m *Manager) Pool(srv config.Server, maxConns int) *Pool {
	if maxConns < 1 {
		maxConns = 1
	}
	m.mu.Lock()
	old := m.pools[srv.Name]
	if old != nil {
		old.mu.Lock()
		same := sameConnection(old.srv, srv)
		if same {
			old.srv = srv
		}
		old.mu.Unlock()
		if same {
			m.mu.Unlock()
			old.setMax(maxConns)
			return old
		}
	}
	p := newPool(m, srv, maxConns)
	if m.closed {
		p.Close()
	}
	m.pools[srv.Name] = p
	m.mu.Unlock()
	if old != nil {
		old.Close()
	}
	return p
}

// Unlock asks for the master password (again after a wrong one) if the credentials file is encrypted and locked.
func (m *Manager) Unlock(ctx context.Context) error {
	return m.unlock(ctx, nil)
}

func (m *Manager) unlock(ctx context.Context, g *gate) error {
	cs := m.opts.Credentials
	if cs == nil || !cs.Encrypted() || cs.Unlocked() {
		return nil
	}
	g.pause()
	defer g.resume()
	if err := m.acquire(ctx); err != nil {
		return err
	}
	defer m.release()
	retry := false
	for !cs.Unlocked() {
		ans, err := m.callSecret(ctx, SecretRequest{Kind: SecretMaster, Retry: retry})
		if err != nil {
			return err
		}
		if err := cs.Unlock(ans.Value); err != nil {
			if errs.KindOf(err) != errs.WrongMasterPassword {
				return err
			}
			retry = true
		}
	}
	return nil
}

// ForgetSecrets drops the secrets typed in this session for server ("" = all servers).
func (m *Manager) ForgetSecrets(server string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if server == "" {
		m.secrets = map[string]string{}
		return
	}
	for _, k := range []SecretKind{SecretPassword, SecretPassphrase} {
		delete(m.secrets, secretKey(server, k))
	}
}

// Close closes all pools.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	pools := make([]*Pool, 0, len(m.pools))
	for _, p := range m.pools {
		pools = append(pools, p)
	}
	m.mu.Unlock()
	for _, p := range pools {
		p.Close()
	}
}

func sameConnection(a, b config.Server) bool {
	return a.Host == b.Host && a.Port == b.Port && a.User == b.User && a.Auth == b.Auth && a.KeyFile == b.KeyFile
}

// RenamePool preserves active connections when a server is renamed.
func (m *Manager) RenamePool(oldName, newName string) {
	if oldName == newName {
		return
	}
	m.mu.Lock()
	p := m.pools[oldName]
	if p == nil {
		m.mu.Unlock()
		return
	}
	// A pool created under the new name before the rename (a request racing the edit) is replaced; close it.
	stale := m.pools[newName]
	p.mu.Lock()
	p.srv.Name = newName
	p.mu.Unlock()
	m.pools[newName] = p
	delete(m.pools, oldName)
	m.mu.Unlock()
	if stale != nil {
		stale.Close()
	}
}
