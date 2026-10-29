package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func buildBinary(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "spiid")
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", p, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %v: %s", err, out)
	}
	return p
}

func command(t *testing.T, binary string, args ...string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, binary, args...)
	home := t.TempDir()
	cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+home, "AppData="+home)
	return cmd
}

func TestCLIUsageAndPort(t *testing.T) {
	binary := buildBinary(t)
	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"--help"}, 0, "usage:"}, {[]string{"--unknown"}, 2, "usage:"}, {[]string{"serve"}, 2, "usage:"}, {
			[]string{"version"},
			0,
			"0.1.0",
		},
	} {
		cmd := command(t, binary, tc.args...)
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			if e, ok := err.(*exec.ExitError); ok {
				code = e.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if code != tc.code || !strings.Contains(string(out), tc.want) {
			t.Fatalf("%v: %d %s", tc.args, code, out)
		}
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	out, err := command(t, binary, "--no-browser", "--port", port).CombinedOutput()
	e, ok := err.(*exec.ExitError)
	if !ok || e.ExitCode() != 1 || !strings.Contains(string(out), "cannot listen on 127.0.0.1:"+port) {
		t.Fatalf("%s %v", out, err)
	}
}

func TestCLIForegroundShutdown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGINT subprocess test requires Unix")
	}
	binary := buildBinary(t)
	for _, reason := range []string{"signal", "quit"} {
		t.Run(reason, func(t *testing.T) {
			var logs bytes.Buffer
			cmd := command(t, binary, "--no-browser", "--port", "0")
			cmd.Stderr = &logs
			output, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if cmd.ProcessState == nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()
			scanner := bufio.NewScanner(output)
			if !scanner.Scan() {
				t.Fatal("missing startup URL")
			}
			line := scanner.Text()
			raw := regexp.MustCompile(`http://127\.0\.0\.1:\d+/#t=[a-f0-9]{64}`).FindString(line)
			if raw == "" || !strings.Contains(line, "press Ctrl+C to quit") {
				t.Fatal(line)
			}
			u, _ := url.Parse(raw)
			client := http.Client{Timeout: time.Second}
			defer client.CloseIdleConnections()
			if reason == "quit" {
				req, _ := http.NewRequest("POST", "http://"+u.Host+"/api/call",
					strings.NewReader(`{"method":"app.quit","params":{}}`))
				req.Header.Set("Origin", "http://"+u.Host)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer "+strings.TrimPrefix(u.Fragment, "t="))
				r, e := client.Do(req)
				if e != nil {
					t.Fatal(e)
				}
				body, e := io.ReadAll(r.Body)
				r.Body.Close()
				if e != nil || r.StatusCode != 200 || strings.TrimSpace(string(body)) != `{"result":{}}` {
					t.Fatalf("quit: %s %v", body, e)
				}
			} else if err = cmd.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			if err = cmd.Wait(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(logs.String(), "stopped") ||
				strings.Contains(logs.String(), strings.TrimPrefix(u.Fragment, "t=")) {
				t.Fatal(logs.String())
			}
		})
	}
}

func TestBrowserFile(t *testing.T) {
	done := make(chan struct{})
	defer close(done)
	path, err := browserFile("http://127.0.0.1:1234/#t=private&x=1", done)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	defer os.RemoveAll(dir)
	for _, tc := range []struct {
		path string
		mode os.FileMode
	}{{dir, 0700}, {path, 0600}} {
		info, err := os.Stat(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != tc.mode {
			t.Fatal(info.Mode())
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := `<!doctype html><meta charset="utf-8"><meta http-equiv="refresh" ` +
		`content="0;url=http://127.0.0.1:1234/#t=private&amp;x=1">`
	if string(data) != want {
		t.Fatal(string(data))
	}
	previous := openBrowser
	defer func() { openBrowser = previous }()
	openBrowser = func(got string) error {
		if got != path || strings.Contains(got, "private") {
			t.Fatal(got)
		}
		return nil
	}
	if err := openBrowser(path); err != nil {
		t.Fatal(err)
	}
	// A separate signal allows observing shutdown cleanup before returning.
	shutdown := make(chan struct{})
	other, err := browserFile("http://127.0.0.1:1234/#t=private", shutdown)
	if err != nil {
		t.Fatal(err)
	}
	close(shutdown)
	deadline := time.Now().Add(time.Second)
	for {
		_, err := os.Stat(filepath.Dir(other))
		if os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("browser folder retained after shutdown")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := os.Stat(other); !os.IsNotExist(err) {
		t.Fatal("browser file retained", err)
	}
}
