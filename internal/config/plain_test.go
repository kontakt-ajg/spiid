package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"spiid/internal/errs"
	"spiid/internal/logx"
)

func TestPlainSecretsAndConcurrentServers(t *testing.T) {
	s := NewStore(t.TempDir(), t.TempDir())
	if _, err := os.Stat(filepath.Join(s.dir, "config.json")); !os.IsNotExist(err) {
		t.Fatal("created before save")
	}
	list := []Server{
		{Name: "password", Host: "host", User: "user", Auth: AuthPassword},
		{Name: "key", Host: "host", User: "user", Auth: AuthKey, KeyFile: "~/id"},
		{Name: "agent", Host: "host", User: "user", Auth: AuthAgent},
	}
	if err := s.SaveServers(list); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, v := range list[:2] {
		wg.Add(1)
		go func(v Server) {
			defer wg.Done()
			kind := Password
			if v.Auth == AuthKey {
				kind = Passphrase
			}
			if err := s.Set(v.Name, kind, "same-private-value"); err != nil {
				t.Error(err)
			}
		}(v)
	}
	wg.Wait()
	data, _ := os.ReadFile(filepath.Join(s.dir, "config.json"))
	if strings.Count(string(data), "same-private-value") != 2 {
		t.Fatal("plain values not saved")
	}
	s = reopen(t, s)
	if value, ok, err := s.Get("key", Passphrase); err != nil || !ok || value != "same-private-value" {
		t.Fatal("lost passphrase")
	}
	if err := s.Protect("first-master"); err != nil {
		t.Fatal(err)
	}
	if s.file.Servers[0].Secret == s.file.Servers[1].Secret {
		t.Fatal("nonce reused across values")
	}
	if s.file.Servers[2].Secret != "" {
		t.Fatal("agent contains secret")
	}
	data, _ = os.ReadFile(filepath.Join(s.dir, "config.json"))
	if bytes.Contains(data, []byte("same-private-value")) {
		t.Fatal("plaintext in protected file")
	}
	s = reopen(t, s)
	if err := s.Set("password", Password, "replacement"); errs.KindOf(err) != errs.Locked {
		t.Fatal(err)
	}
	if err := s.Reset(); err != nil {
		t.Fatal(err)
	}
	s = reopen(t, s)
	for _, v := range s.file.Servers {
		if v.Secret != "" {
			t.Fatal("reset retained secret")
		}
	}
	if err := s.Set("password", Password, "new-value"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("password", Password); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("password", Password); err != nil {
		t.Fatal(err)
	}
	requireSecret(t, reopen(t, s), "password", "")
	for _, name := range []string{"", "a:b"} {
		if _, _, err := s.Get(name, Password); errs.KindOf(err) != errs.InvalidInput {
			t.Fatal(err)
		}
		if err := s.Set(name, Password, "value"); errs.KindOf(err) != errs.InvalidInput {
			t.Fatal(err)
		}
	}
}
func TestLogsOnlySuccessfulCreation(t *testing.T) {
	var output bytes.Buffer
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	s, err := Open(path, logx.New(&output, false))
	if err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSettings(DefaultSettings(dir)); err == nil {
		t.Fatal("expected failure")
	}
	if output.Len() != 0 {
		t.Fatal("logged failed write")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.SaveSettings(DefaultSettings(dir)); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Count(output.String(), "config: config.json created") != 1 {
		t.Fatal(output.String())
	}
}
