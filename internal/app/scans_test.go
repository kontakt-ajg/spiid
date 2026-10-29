package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"spiid/internal/config"
)

func TestCancelScanKeepsDiscoveredFiles(t *testing.T) {
	e := newEnv(t, sftpOpts())
	for i := range 300 {
		e.remote(fmt.Sprintf("tree/d%03d/file", i), 1)
	}
	s := e.ready(1, 1)
	e.trust()
	if err := s.AddServer(context.Background(), e.server(config.AuthPassword), "secret"); err != nil {
		t.Fatal(err)
	}
	if err := s.Download("box", []string{"/tree"}, false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var scan Scan
	for {
		scans := s.Scans()
		if len(scans) == 1 && scans[0].Files > 0 {
			scan = scans[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no partial scan observed")
		}
		time.Sleep(time.Millisecond)
	}
	if scan.Files >= 300 {
		t.Fatalf("scan was not partial: %+v", scan)
	}
	if err := s.CancelScan(scan.ID); err != nil {
		t.Fatal(err)
	}
	s.walks.Wait()
	if len(s.Scans()) != 0 {
		t.Fatal("finished scan still visible")
	}
	if len(s.Jobs()) < scan.Files {
		t.Fatal("discovered jobs removed")
	}
	messages := messageTexts(s)
	if len(messages) != 1 || !strings.HasPrefix(messages[0], "Scan of /tree canceled: ") {
		t.Fatalf("messages: %v", messages)
	}
}
