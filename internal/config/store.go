package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"spiid/internal/errs"
	"spiid/internal/logx"
)

// Store holds config.json in memory: settings, servers and their secrets. All methods are safe for
// concurrent use; every change is written to disk at once. Encrypted secrets are copied through
// unchanged while the master password is not known.
type Store struct {
	mu        sync.Mutex
	dir, home string
	log       *logx.Logger
	loaded    bool
	loadErr   error
	file      configFile
	key       []byte
}
type savedServer struct {
	Server
	Secret string `json:"secret,omitempty"`
}
type configFile struct {
	Version  int           `json:"version"`
	Settings Settings      `json:"settings"`
	Master   *masterParams `json:"master"`
	Servers  []savedServer `json:"servers"`
}

// NewStore returns a Store for config.json in dir; it is read on first use. home expands ~ in paths.
func NewStore(dir, home string) *Store { return &Store{dir: dir, home: home} }

// Open reads the config file at path (a missing file means defaults). The optional home expands ~ in
// paths; it defaults to the config folder.
func Open(path string, log *logx.Logger, home ...string) (*Store, error) {
	s := NewStore(filepath.Dir(path), filepath.Dir(path))
	if len(home) > 0 {
		s.home = home[0]
	}
	s.log = log
	s.load()
	return s, s.loadErr
}
func (s *Store) Dir() string { return s.dir }
func (s *Store) load() error {
	if s.loaded {
		return s.loadErr
	}
	s.loaded = true
	s.file = configFile{Version: 1, Settings: DefaultSettings(s.home), Servers: []savedServer{}}
	path := filepath.Join(s.dir, "config.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s.log.Info("config: config.json not found — starting with no servers")
		return nil
	}
	if err == nil {
		if st, e := os.Stat(path); e == nil && runtime.GOOS != "windows" && st.Mode().Perm()&0044 != 0 {
			s.log.Warn("config: config.json is group- or world-readable")
		}
		err = json.Unmarshal(data, &s.file)
		if err == nil {
			err = s.validateFile(data)
		}
	}
	if err != nil {
		s.loadErr = errs.New(errs.InvalidInput, "load config", path, err)
	}
	return s.loadErr
}
func (s *Store) LoadServers() ([]Server, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return nil, err
	}
	list := make([]Server, len(s.file.Servers))
	for i, v := range s.file.Servers {
		list[i] = v.Server
	}
	return list, nil
}
func (s *Store) LoadSettings() (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.load()
	return s.file.Settings, err
}
func validateServers(list []Server) error {
	for i := range list {
		list[i].Normalize()
		if err := list[i].Validate(); err != nil {
			return err
		}
		for j := 0; j < i; j++ {
			if strings.EqualFold(list[j].Name, list[i].Name) {
				return errs.New(errs.InvalidInput, "save servers", list[i].Name, errors.New("server name used twice"))
			}
		}
	}
	return nil
}
func (s *Store) SaveServers(list []Server) error { return s.EditServers(list, "", "", nil) }

// EditServers saves a server edit and its secret in a single transaction.
func (s *Store) EditServers(list []Server, oldName, newName string, secret *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return err
	}
	list = append([]Server(nil), list...)
	if err := validateServers(list); err != nil {
		return err
	}
	next := s.file
	next.Servers = make([]savedServer, len(list))
	for i, v := range list {
		next.Servers[i].Server = v
		name := v.Name
		if oldName != "" && v.Name == newName {
			name = oldName
		}
		for _, old := range s.file.Servers {
			if old.Name == name && old.Auth == v.Auth {
				next.Servers[i].Secret = old.Secret
			}
		}
		if v.Auth == AuthAgent {
			continue
		}
		if secret != nil && name == oldName {
			value := *secret
			if value != "" && next.Master != nil {
				if s.key == nil {
					return errs.New(errs.Locked, "save secret", "", nil)
				}
				var err error
				value, err = seal(value, s.key)
				if err != nil {
					return err
				}
			}
			next.Servers[i].Secret = value
		}
	}
	if err := s.write(next); err != nil {
		return err
	}
	return nil
}
func (s *Store) SaveSettings(st Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return err
	}
	st.Normalize(s.home)
	if !filepath.IsAbs(st.DownloadDir) {
		return errs.New(errs.InvalidInput, "save settings", "", errors.New("download_dir must be absolute"))
	}
	next := s.file
	next.Settings = st
	if err := s.write(next); err != nil {
		return err
	}
	s.log.Info("config: settings saved (download folder %s, %d threads, %d parallel files)", st.DownloadDir,
		st.ThreadsPerFile, st.ParallelFiles)
	return nil
}
