package engine

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"spiid/internal/logx"
)

func TestLogsEachDownloadTransition(t *testing.T) {
	var output bytes.Buffer
	e, _ := slowEnv(t, func(o *Options) { o.Log = logx.New(&output, false) })
	j := e.add("r.bin")
	e.waitPartial(j.ID)
	if err := e.m.Cancel(j.ID); err != nil {
		t.Fatal(err)
	}
	e.waitState(j.ID, Canceled)
	e.pool.setDelay(0)
	if err := e.m.Retry(j.ID); err != nil {
		t.Fatal(err)
	}
	e.waitState(j.ID, Done)
	// Reading output after Close also joins any final transition logging.
	e.m.Close()
	for line, count := range map[string]int{
		"download: added box:/r.bin → ":          1,
		"download: started /r.bin (2 segments)":  2,
		"download: canceled /r.bin":              1,
		"download: done /r.bin (4194304 bytes, ": 1,
		" B/s average)":                          1,
	} {
		if got := strings.Count(output.String(), line); got != count {
			t.Fatalf("%q: got %d, want %d\n%s", line, got, count, output.String())
		}
	}
}

func TestLogsFailureRetryAndSkip(t *testing.T) {
	var output bytes.Buffer
	e := newEnv(t, func(o *Options) { o.Log = logx.New(&output, false) })
	want := e.remoteFile("file", 100)
	req := e.req("file")
	if err := os.WriteFile(req.Local, []byte("different"), 0600); err != nil {
		t.Fatal(err)
	}
	j, _ := e.m.Add(req)
	e.waitState(j.ID, Failed)
	if err := e.m.Overwrite(j.ID); err != nil {
		t.Fatal(err)
	}
	e.waitState(j.ID, Done)
	equalFile(t, req.Local, want)
	e.m.Close()
	// A new manager has a fresh queue and recognizes the completed local file.
	e.m = e.newManager(func(o *Options) { o.Log = logx.New(&output, false) })
	e.waitState(e.add("file").ID, Skipped)
	e.m.Close()
	for line, count := range map[string]int{
		"download: added box:/file → ":                 2,
		"ERROR  download: failed /file (":              1,
		"download: started /file (1 segments)":         1,
		"download: done /file (100 bytes, ":            1,
		"download: skipped /file (already downloaded)": 1,
	} {
		if got := strings.Count(output.String(), line); got != count {
			t.Fatalf("%q: got %d, want %d\n%s", line, got, count, output.String())
		}
	}
}

func TestEmptyFileLogsStartedOnlyAfterCreation(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			e := newEnv(t, func(o *Options) { o.Log = logx.New(&output, false) })
			e.remoteFile("empty", 0)
			req := e.req("empty")
			req.Overwrite = true
			if fail {
				if err := os.Mkdir(req.Local, 0700); err != nil {
					t.Fatal(err)
				}
			}
			j, _ := e.m.Add(req)
			state := Done
			if fail {
				state = Failed
			}
			e.waitState(j.ID, state)
			e.m.Close()
			log := output.String()
			started := "download: started /empty (0 segments)"
			if fail {
				if strings.Contains(log, started) {
					t.Fatal("failed creation logged started:", log)
				}
				if !strings.Contains(log, "ERROR  download: failed /empty (") {
					t.Fatal(log)
				}
			} else {
				if n := strings.Count(log, started); n != 1 {
					t.Fatalf("got %d start logs: %s", n, log)
				}
				if !strings.Contains(log, "download: done /empty (0 bytes, ") {
					t.Fatal(log)
				}
				if strings.Index(log, started) > strings.Index(log, "download: done") {
					t.Fatal(log)
				}
			}
		})
	}
}
