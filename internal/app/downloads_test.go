package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"spiid/internal/config"
	"spiid/internal/errs"
	"spiid/internal/sftptest"
)

func TestDownloadValidatesSynchronously(t *testing.T) {
	e := newEnv(t, sftpOpts())
	s := e.ready(1, 1)
	if err := s.Download("nope", []string{"/a"}, false); errs.KindOf(err) != errs.InvalidInput {
		t.Fatalf("unknown server: %v", err)
	}
	if err := s.AddServer(context.Background(), e.server(config.AuthAgent), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Download("box", nil, false); errs.KindOf(err) != errs.InvalidInput {
		t.Fatalf("no paths: %v", err)
	}
}

func TestDownloadWalkErrorMessage(t *testing.T) {
	e := newEnv(t, sftpOpts())
	s := e.ready(1, 1)
	e.trust()
	if err := s.AddServer(context.Background(), e.server(config.AuthPassword), "secret"); err != nil {
		t.Fatal(err)
	}
	if err := s.Download("box", []string{"/missing", "/other"}, false); err != nil {
		t.Fatal(err)
	}
	deadline := 0
	for len(messageTexts(s)) == 0 {
		if deadline++; deadline > 3000 {
			t.Fatal("no message")
		}
		waitTick()
	}
	m := messageTexts(s)[0]
	if !strings.HasPrefix(m, "Scan of /missing (and 1 more) stopped: ") {
		t.Fatalf("message %q", m)
	}
	s.walks.Wait()
	if err := s.Download("box", []string{"/missing"}, false); err != nil {
		t.Fatal(err)
	}
	for len(messageTexts(s)) < 2 {
		if deadline++; deadline > 3000 {
			t.Fatal("no second message")
		}
		waitTick()
	}
	if m := messageTexts(s)[1]; !strings.HasPrefix(m, "Scan of /missing stopped: ") {
		t.Fatalf("message %q", m)
	}
}

func TestSummary(t *testing.T) {
	e := newEnv(t, sftptest.Options{ReadDelay: 10 * time.Millisecond})
	e.remote("a.bin", 3<<20)
	e.remote("b.bin", 3<<20)
	s := e.ready(1, 1)
	e.trust()
	if got := s.Summary(); got != (Summary{}) {
		t.Fatalf("empty summary %+v", got)
	}
	if err := s.AddServer(context.Background(), e.server(config.AuthPassword), "secret"); err != nil {
		t.Fatal(err)
	}
	if err := s.Download("box", []string{"/a.bin", "/b.bin"}, false); err != nil {
		t.Fatal(err)
	}
	var sum Summary
	for i := 0; ; i++ {
		if sum = s.Summary(); sum.Active == 1 && sum.Queued == 1 && sum.Speed > 0 {
			break
		}
		if i > 3000 {
			t.Fatalf("summary never showed 1 active + 1 queued with speed: %+v, jobs %+v", sum, s.Jobs())
		}
		waitTick()
	}
	for _, j := range s.Jobs() {
		if err := s.Cancel(j.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSelectionOfManyFilesIsOneScan(t *testing.T) {
	e := newEnv(t, sftptest.Options{})
	for _, name := range []string{"a.bin", "b.bin", "c.bin"} {
		e.remote(name, 10)
	}
	s := e.ready(1, 1)
	e.trust()
	if err := s.AddServer(context.Background(), e.server(config.AuthPassword), "secret"); err != nil {
		t.Fatal(err)
	}
	if err := s.Download("box", []string{"/a.bin", "/b.bin", "/c.bin"}, false); err != nil {
		t.Fatal(err)
	}
	settle(t, s, 3)
	s.walks.Wait()
	if got := messageTexts(s); len(got) != 1 || got[0] != "Scanned /a.bin (and 2 more): 3 files queued" {
		t.Fatalf("messages %q", got)
	}
}
