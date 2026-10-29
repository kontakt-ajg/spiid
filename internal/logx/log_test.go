package logx

import (
	"bytes"
	"regexp"
	"strings"
	"sync"
	"testing"
)

func TestControlCharacters(t *testing.T) {
	var b bytes.Buffer
	var controls strings.Builder
	for r := rune(0); r <= 0x9f; r++ {
		if r < 0x20 || r >= 0x7f {
			controls.WriteRune(r)
		}
	}
	New(&b, false).Info("file\x1b[31m\a%s終", controls.String())
	want := "  INFO  file [31m " + strings.Repeat(" ", 65) + "終\n"
	if !strings.HasSuffix(b.String(), want) {
		t.Fatalf("control characters were not replaced: %q", b.String())
	}
}

func TestFormatLevelsConcurrency(t *testing.T) {
	var b bytes.Buffer
	l := New(&b, false)
	l.Info("hello %d", 7)
	l.Warn("warning")
	l.Error("error")
	l.Debug("hidden")
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 3 {
		t.Fatal(b.String())
	}
	for i, level := range []string{"INFO", "WARN", "ERROR"} {
		if !regexp.MustCompile(`^\d{2}:\d{2}:\d{2}  ` + level + `  .+$`).MatchString(lines[i]) {
			t.Fatal(lines[i])
		}
	}
	b.Reset()
	l = New(&b, true)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); l.Debug("line") }()
	}
	wg.Wait()
	if strings.Count(b.String(), "  DEBUG  line\n") != 40 {
		t.Fatal(b.String())
	}
}
