package sshconn

import (
	"crypto/ed25519"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
	"spiid/internal/config"
	"spiid/internal/sftptest"
)

func keyEnv(t *testing.T, pass string) *env {
	t.Helper()
	priv, pub := newKey(t)
	e := newEnv(t, sftptest.Options{User: "alice", Password: "unused", AuthorizedKeys: []ssh.PublicKey{pub}})
	e.trust(e.ss.KnownHostsLine())
	var blk *pem.Block
	var err error
	if pass == "" {
		blk, err = ssh.MarshalPrivateKey(ed25519.PrivateKey(priv), "")
	} else {
		blk, err = ssh.MarshalPrivateKeyWithPassphrase(ed25519.PrivateKey(priv), "", []byte(pass))
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.home, "id"), pem.EncodeToMemory(blk), 0o600); err != nil {
		t.Fatal(err)
	}
	e.srv.Auth = config.AuthKey
	e.srv.KeyFile = "~/id"
	if err := e.creds.SaveServers([]config.Server{e.srv}); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestKeyFile(t *testing.T) {
	e := keyEnv(t, "")
	c, err := get(t, e.manager(nil), e)
	if err != nil {
		t.Fatal(err)
	}
	mustStat(t, c)
}

func TestKeyFilePassphrase(t *testing.T) {
	t.Run("saved", func(t *testing.T) {
		e := keyEnv(t, "pp")
		e.creds.Set("box", config.Passphrase, "pp")
		c, err := get(t, e.manager(nil), e)
		if err != nil {
			t.Fatal(err)
		}
		mustStat(t, c)
		if _, s := e.p.counts(); s != 0 {
			t.Fatalf("%d prompts", s)
		}
	})
	t.Run("prompt", func(t *testing.T) {
		e := keyEnv(t, "pp")
		e.p.secret = func(r SecretRequest, n int) (SecretAnswer, error) {
			if n == 1 {
				return SecretAnswer{Value: "bad"}, nil
			}
			return SecretAnswer{Value: "pp", Save: true}, nil
		}
		c, err := get(t, e.manager(nil), e)
		if err != nil {
			t.Fatal(err)
		}
		mustStat(t, c)
		reqs := e.p.secretReqs()
		if len(reqs) != 2 || reqs[0].Kind != SecretPassphrase || !reqs[0].OfferSave || reqs[0].Retry || !reqs[1].Retry {
			t.Fatalf("requests = %+v", reqs)
		}
		if v, _, _ := e.creds.Get("box", config.Passphrase); v != "pp" {
			t.Fatalf("passphrase saved as %q", v)
		}
	})
}
