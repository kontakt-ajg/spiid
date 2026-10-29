package sshconn

import (
	"context"
	"path/filepath"
	"testing"

	"spiid/internal/config"
	"spiid/internal/sftptest"
)

func TestUnlockMaster(t *testing.T) {
	e := newEnv(t, sftptest.Options{})
	e.ready()
	if err := e.creds.Protect("master-password"); err != nil {
		t.Fatal(err)
	}
	locked, err := config.Open(filepath.Join(e.home, "config.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !locked.Encrypted() || locked.Unlocked() {
		t.Fatal("credentials should be encrypted and locked")
	}
	e.creds = locked
	e.p.secret = func(r SecretRequest, n int) (SecretAnswer, error) {
		if r.Kind != SecretMaster {
			t.Errorf("unexpected %+v", r)
		}
		if n == 1 {
			return SecretAnswer{Value: "bad"}, nil
		}
		return SecretAnswer{Value: "master-password"}, nil
	}
	p := e.manager(nil).Pool(e.srv, 1)
	c, err := p.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	mustStat(t, c)
	reqs := e.p.secretReqs()
	if len(reqs) != 2 || reqs[0].Retry || !reqs[1].Retry {
		t.Fatalf("requests = %+v", reqs)
	}
	p.Discard(c)
	if c, err = p.Get(context.Background()); err != nil {
		t.Fatal(err)
	}
	mustStat(t, c)
	if _, s := e.p.counts(); s != 2 {
		t.Fatalf("%d prompts after the second dial, want 2 in total", s)
	}
}
