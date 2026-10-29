package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"spiid/internal/errs"
	"spiid/internal/logx"
)

func secretStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore(t.TempDir(), t.TempDir())
	servers := []Server{{Name: "box", Host: "host", User: "user", Auth: AuthPassword}}
	if err := s.SaveServers(servers); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("box", Password, "private-password"); err != nil {
		t.Fatal(err)
	}
	return s
}
func reopen(t *testing.T, s *Store) *Store {
	t.Helper()
	next, err := Open(filepath.Join(s.dir, "config.json"), nil, s.home)
	if err != nil {
		t.Fatal(err)
	}
	return next
}
func requireSecret(t *testing.T, s *Store, name, want string) {
	t.Helper()
	got, ok, err := s.Get(name, Password)
	if err != nil || got != want || ok != (want != "") {
		t.Fatalf("secret mismatch (exists=%v, err=%v)", ok, err)
	}
}
func TestMasterLifecycle(t *testing.T) {
	s := secretStore(t)
	requireSecret(t, reopen(t, s), "box", "private-password")
	for _, password := range []string{"", "short", "1234567"} {
		if err := s.Protect(password); errs.KindOf(err) != errs.InvalidInput {
			t.Fatalf("short password: %v", err)
		}
	}
	if err := s.Protect("first-master"); err != nil {
		t.Fatal(err)
	}
	if err := s.Protect("first-master"); err == nil {
		t.Fatal("protected twice")
	}
	locked := reopen(t, s)
	if _, _, err := locked.Get("box", Password); errs.KindOf(err) != errs.Locked {
		t.Fatal(err)
	}
	if err := locked.Unlock("wrong-master"); errs.KindOf(err) != errs.WrongMasterPassword {
		t.Fatal(err)
	}
	if err := locked.Unlock("first-master"); err != nil {
		t.Fatal(err)
	}
	requireSecret(t, locked, "box", "private-password")
	salt := append([]byte(nil), locked.file.Master.Salt...)
	for _, op := range []func() error{
		func() error { return locked.ChangeMaster("wrong", "second-master") },
		func() error { return locked.Unprotect("wrong") },
	} {
		if err := op(); errs.KindOf(err) != errs.WrongMasterPassword ||
			!strings.Contains(err.Error(), "wrong master password") {
			t.Fatalf("wrong current: %v", err)
		}
	}
	if err := locked.ChangeMaster("first-master", "short"); errs.KindOf(err) != errs.InvalidInput {
		t.Fatal(err)
	}
	if err := locked.ChangeMaster("first-master", "second-master"); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(salt, locked.file.Master.Salt) {
		t.Fatal("salt reused")
	}
	locked = reopen(t, locked)
	if err := locked.Unlock("first-master"); err == nil {
		t.Fatal("old password accepted")
	}
	if err := locked.Unprotect("second-master"); err != nil {
		t.Fatal(err)
	}
	if locked.Encrypted() {
		t.Fatal("still encrypted")
	}
	requireSecret(t, reopen(t, locked), "box", "private-password")
}
func TestLockedMetadataPreservesCiphertext(t *testing.T) {
	s := secretStore(t)
	if err := s.Protect("first-master"); err != nil {
		t.Fatal(err)
	}
	ciphertext := s.file.Servers[0].Secret
	s = reopen(t, s)
	settings, _ := s.LoadSettings()
	settings.Theme = ThemeDark
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	list, _ := s.LoadServers()
	list[0].Name = "renamed"
	list[0].StartDir = "/elsewhere"
	if err := s.EditServers(list, "box", "renamed", nil); err != nil {
		t.Fatal(err)
	}
	if s.Unlocked() || s.file.Servers[0].Secret != ciphertext {
		t.Fatal("metadata changed encryption")
	}
	if err := s.Unlock("first-master"); err != nil {
		t.Fatal(err)
	}
	requireSecret(t, s, "renamed", "private-password")
	old := s.file.Servers[0].Secret
	if err := s.Set("renamed", Password, "private-password"); err != nil {
		t.Fatal(err)
	}
	if old == s.file.Servers[0].Secret {
		t.Fatal("nonce reused")
	}
	if err := s.Reset(); err != nil {
		t.Fatal(err)
	}
	s = reopen(t, s)
	if s.Encrypted() {
		t.Fatal("reset kept master")
	}
	requireSecret(t, s, "renamed", "")
	if s.file.Settings.Theme != ThemeDark || len(s.file.Servers) != 1 {
		t.Fatal("reset lost metadata")
	}
}
func TestDamagedEncryptedValues(t *testing.T) {
	s := secretStore(t)
	if err := s.Protect("first-master"); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(filepath.Join(s.dir, "config.json"))
	for _, bad := range []string{"!base64", base64.StdEncoding.EncodeToString([]byte("short"))} {
		f := s.file
		f.Servers = append([]savedServer(nil), f.Servers...)
		f.Servers[0].Secret = bad
		data, _ := json.Marshal(f)
		os.WriteFile(filepath.Join(s.dir, "config.json"), data, 0600)
		if _, err := Open(filepath.Join(s.dir, "config.json"), nil); err == nil {
			t.Fatal("damaged value accepted")
		}
	}
	os.WriteFile(filepath.Join(s.dir, "config.json"), original, 0600)
	s = reopen(t, s)
	data, _ := base64.StdEncoding.DecodeString(s.file.Servers[0].Secret)
	data[len(data)-1] ^= 1
	s.file.Servers[0].Secret = base64.StdEncoding.EncodeToString(data)
	if err := s.Unlock("first-master"); err == nil || errs.KindOf(err) == errs.WrongMasterPassword {
		t.Fatalf("tampering must report damaged secret: %v", err)
	}
}
func TestConfigWarningAndSecretFreeLogs(t *testing.T) {
	var b bytes.Buffer
	s := secretStore(t)
	s.log = logx.New(&b, false)
	if err := s.Protect("first-master"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.dir, "config.json")
	os.Chmod(path, 0640)
	if _, err := Open(path, s.log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "WARN") || !strings.Contains(b.String(), "group- or world-readable") {
		t.Fatal("missing permission warning")
	}
	for _, value := range []string{"private-password", "first-master", s.file.Servers[0].Secret} {
		if strings.Contains(b.String(), value) {
			t.Fatal("secret in logs")
		}
	}
	entries, _ := os.ReadDir(s.dir)
	if len(entries) != 1 || entries[0].Name() != "config.json" {
		t.Fatal("not a single file")
	}
}
