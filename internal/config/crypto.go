package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"
	"spiid/internal/errs"
)

type masterParams struct {
	Salt      []byte `json:"salt"`
	Time      uint32 `json:"time"`
	MemoryKiB uint32 `json:"memory_kib"`
	Threads   uint8  `json:"threads"`
	Check     string `json:"check"`
}

var defaultKDF = masterParams{Time: 3, MemoryKiB: 65536, Threads: 4}

func (m masterParams) validate() error {
	if len(m.Salt) != 16 || m.Time < 1 || m.Time > 10 || m.MemoryKiB < 8192 || m.MemoryKiB > 1048576 ||
		m.Threads < 1 || m.Threads > 16 {
		return errors.New("invalid master KDF parameters")
	}
	return validCipher(m.Check)
}
func (m masterParams) derive(password string) []byte {
	return argon2.IDKey([]byte(password), m.Salt, m.Time, m.MemoryKiB, m.Threads, 32)
}
func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func seal(value string, key []byte) (string, error) {
	a, err := gcm(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, 12)
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(a.Seal(nonce, nonce, []byte(value), []byte("spiid-secret-v1"))), nil
}
func validCipher(value string) error {
	data, err := base64.StdEncoding.Strict().DecodeString(value)
	if err != nil {
		return fmt.Errorf("invalid encrypted value: %w", err)
	}
	if len(data) < 28 {
		return errors.New("truncated encrypted value")
	}
	return nil
}
func unseal(value string, key []byte) (string, error) {
	if err := validCipher(value); err != nil {
		return "", err
	}
	data, _ := base64.StdEncoding.Strict().DecodeString(value)
	a, err := gcm(key)
	if err != nil {
		return "", err
	}
	plain, err := a.Open(nil, data[:12], data[12:], []byte("spiid-secret-v1"))
	if err != nil {
		return "", errors.New("saved secret decryption failed")
	}
	return string(plain), nil
}
func (s *Store) validateFile(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, name := range []string{"version", "settings", "master", "servers"} {
		if len(fields[name]) == 0 || (name != "master" && string(fields[name]) == "null") {
			return fmt.Errorf("missing %s", name)
		}
	}
	if err := requiredFields(fields["settings"], "download_dir", "threads_per_file", "parallel_files",
		"max_connections", "theme"); err != nil {
		return err
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(fields["servers"], &entries); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := requiredFields(entry, "name", "host", "port", "user", "auth"); err != nil {
			return err
		}
	}
	if s.file.Version != 1 {
		return errors.New("unsupported config version")
	}
	if s.file.Master != nil {
		if err := s.file.Master.validate(); err != nil {
			return err
		}
	}
	list := make([]Server, len(s.file.Servers))
	for i, v := range s.file.Servers {
		list[i] = v.Server
		if v.Auth == AuthAgent && v.Secret != "" {
			return errors.New("agent server contains a secret")
		}
		if s.file.Master != nil && v.Secret != "" {
			if err := validCipher(v.Secret); err != nil {
				return err
			}
		}
	}
	if err := validateServers(list); err != nil {
		return err
	}
	for i := range list {
		s.file.Servers[i].Server = list[i]
	}
	s.file.Settings.Normalize(s.home)
	return nil
}
func wrongMaster() error {
	return errs.New(errs.WrongMasterPassword, "", "", nil)
}
func (s *Store) verify(current string) ([]byte, error) {
	if s.file.Master == nil {
		return nil, errs.New(errs.InvalidInput, "master password", "", errors.New("not protected"))
	}
	key := s.file.Master.derive(current)
	value, err := unseal(s.file.Master.Check, key)
	if err != nil || value != "spiid" {
		return nil, wrongMaster()
	}
	return key, nil
}

func requiredFields(data []byte, names ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, name := range names {
		if len(fields[name]) == 0 || string(fields[name]) == "null" {
			return fmt.Errorf("missing %s", name)
		}
	}
	return nil
}
