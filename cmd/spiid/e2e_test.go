package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"spiid/internal/webdriver"
)

func TestE2EWebUI(t *testing.T) {
	if os.Getenv("SPIID_E2E") != "1" {
		t.Skip("set SPIID_E2E=1; Safari remote automation must be enabled")
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	remote := filepath.Join(tmp, "remote")
	downloads := filepath.Join(tmp, "downloads")
	for _, d := range []string{home, remote, downloads, filepath.Join(home, ".ssh")} {
		must(os.MkdirAll(d, 0700))
	}
	// Build before replacing HOME so Go uses the toolchain's existing module/build caches.
	goBin, err := exec.LookPath("go")
	if err != nil {
		goBin = filepath.Join(runtime.GOROOT(), "bin", "go")
	}
	for _, name := range []string{"spiid", "spiid-devserver"} {
		cmd := exec.CommandContext(ctx, goBin, "build", "-o", filepath.Join(tmp, name), "./cmd/"+name)
		cmd.Dir = "../.."
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build: %v\n%s", err, out)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	dev := exec.CommandContext(ctx, filepath.Join(tmp, "spiid-devserver"), "-root", remote, "-addr", "127.0.0.1:0",
		"-make-sample", "3", "-read-delay", "20ms")
	dev.Stderr = os.Stderr
	stdout, err := dev.StdoutPipe()
	must(err)
	must(dev.Start())
	defer func() { _ = dev.Process.Kill(); _ = dev.Wait() }()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		t.Fatalf("devserver readiness: %v", scanner.Err())
	}
	addr := regexp.MustCompile(`addr=(\S+)`).FindStringSubmatch(scanner.Text())
	if len(addr) != 2 {
		t.Fatal("missing devserver address")
	}
	host, port, err := net.SplitHostPort(addr[1])
	must(err)
	kh := filepath.Join(home, ".ssh", "known_hosts")
	if _, err = os.Stat(kh); !os.IsNotExist(err) {
		t.Fatal("known_hosts must not be prefilled")
	}
	application := exec.CommandContext(ctx, filepath.Join(tmp, "spiid"), "--no-browser", "--port", "0")
	application.Stderr = os.Stderr
	output, err := application.StdoutPipe()
	must(err)
	must(application.Start())
	appDone := make(chan error, 1)
	go func() { appDone <- application.Wait(); close(appDone) }()
	defer func() { _ = application.Process.Kill(); <-appDone }()
	appScanner := bufio.NewScanner(output)
	if !appScanner.Scan() {
		t.Fatal("missing URL")
	}
	url := regexp.MustCompile(`http://127\.0\.0\.1:\d+/#t=[a-f0-9]+`).FindString(appScanner.Text())
	if url == "" {
		t.Fatal("missing URL")
	}
	wait := func(what string, timeout time.Duration, ready func() bool, interval ...time.Duration) {
		t.Helper()
		poll := 50 * time.Millisecond
		if len(interval) > 0 {
			poll = interval[0]
		}
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			if ready() {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(poll):
			}
		}
		t.Fatalf("timed out waiting for %s", what)
	}
	wd, err := webdriver.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer wd.Close()
	defer func() {
		if t.Failed() {
			path := filepath.Join(tmp, "failure.png")
			if png, e := wd.Screenshot(); e == nil {
				if e = os.WriteFile(path, png, 0600); e == nil {
					t.Logf("failure screenshot: %s", path)
				}
			}
		}
	}()
	must(wd.NewSession())
	must(wd.Navigate(url))
	script := func(js string, args ...any) any { t.Helper(); v, e := wd.ExecuteScript(js, args...); must(e); return v }
	click := func(text string) {
		t.Helper()
		wait("button "+text, 20*time.Second, func() bool {
			return script(`const e=[...document.querySelectorAll('button')].find(e=>e.textContent.trim`+
				`()===arguments[0] && e.getClientRects().length && !e.disabled);if(!e)return`+
				` false;e.click();return true`, text) == true
		})
	}
	text := func(css string) string {
		v := script(`return document.querySelector(arguments[0])?.textContent || ''`, css)
		s, _ := v.(string)
		return s
	}
	click("Add server")
	for _, f := range [][2]string{{"Name", "dev"}, {"Host", host}, {"Port", port}, {"User", "dev"}} {
		e, eErr := wd.Find(fmt.Sprintf(`[data-test=%q]`, f[0]))
		must(eErr)
		must(wd.SendKeys(e, f[1]))
	}
	// Selecting a native option through WebDriver preserves the real UI change handler.
	e, err := wd.Find(`select[aria-label="Login"] option[value="password"]`)
	must(err)
	must(wd.Click(e))
	e, err = wd.Find(`[data-test="Password"]`)
	must(err)
	must(wd.SendKeys(e, "dev"))
	click("Save")
	wait("saved server", 20*time.Second,
		func() bool { return strings.Contains(text(".server-card"), "dev@"+host+":"+port) })
	click("Settings")
	click("Change…")
	wait("folder chooser", 20*time.Second, func() bool { return text(`[data-test="picker-path"]`) != "" })
	for i := 0; i < 40 && text(`[data-test="picker-path"]`) != "/"; i++ {
		click("Up")
	}
	if text(`[data-test="picker-path"]`) != "/" {
		t.Fatal("chooser did not reach root")
	}
	for _, part := range strings.Split(strings.TrimPrefix(downloads, "/"), "/") {
		click(part)
	}
	wait("download folder", 20*time.Second, func() bool { return text(`[data-test="picker-path"]`) == downloads })
	click("Select folder")
	for key, target := range map[string]int{"threads_per_file": 4, "parallel_files": 2} {
		wait(key, 20*time.Second, func() bool {
			return script(`const r=document.querySelector('[data-test="'+arguments[0]+'"]');if(!r)retu`+
				`rn false;const n=Number(r.children[2].textContent);if(n===arguments[1])retu`+
				`rn true;const b=r.querySelectorAll('button')[n<arguments[1]?1:0];if(!b.disa`+
				`bled)b.click();return false`, key, target) == true
		})
	}
	click("Servers")
	wait("server card", 20*time.Second, func() bool {
		e, eErr := wd.Find(`.server-card[aria-label="dev"]`)
		return eErr == nil && wd.Click(e) == nil
	})
	click("Trust")
	for _, name := range []string{"sample-0.bin", "sample-1.bin", "tree"} {
		wait(name, 20*time.Second, func() bool {
			e, eErr := wd.Find(fmt.Sprintf(`input[aria-label=%q]`, "Select "+name))
			return eErr == nil && wd.Click(e) == nil
		})
	}
	if !strings.Contains(text(".selection"), "2 files, 1 folder · 104.9 MB") {
		t.Fatal("wrong selection: ", text(".selection"))
	}
	click("Download")
	click("Downloads")
	done := map[string]bool{}
	sawRunning := false
	running := regexp.MustCompile(`\d+ % · .+ of .+ · \d+(\.\d)? ?(B|KB|MB|GB)/s · \d+ of 4 threads`)
	wait("62 Done jobs", 120*time.Second, func() bool {
		if !sawRunning {
			script(`const n=[...document.querySelectorAll('.virtual')].find(e=>e.getClientRects` +
				`().length);if(n)n.scrollTop=n.scrollHeight`)
		}
		if running.MatchString(text(".page:not([hidden])")) {
			sawRunning = true
		}
		// Read all rendered cards in one call: the virtual list may replace card nodes between two calls.
		cards := script(`return [...document.querySelectorAll('article[data-test-id]')]` +
			`.map(c => [c.getAttribute('data-test-id'), c.getAttribute('data-test-state')])`).([]any)
		for _, c := range cards {
			v := c.([]any)
			done[v[0].(string)] = v[1] == "done"
		}
		// Walk the actual virtual list so every job is observed through its rendered card.
		if sawRunning {
			script(`const n=[...document.querySelectorAll('.virtual')].find(e=>e.getClientRects` +
				`().length);if(n)n.scrollTop=n.scrollTop+n.clientHeight>=n.scrollHeight-2?0:` +
				`n.scrollTop+Math.max(100,n.clientHeight-100)`)
		}
		if len(done) != 62 {
			return false
		}
		for _, v := range done {
			if !v {
				return false
			}
		}
		return true
	}, 100*time.Millisecond)
	if !sawRunning {
		t.Fatal("never saw running speed and of 4 threads")
	}
	wait("idle header and rail", 10*time.Second, func() bool {
		return strings.Contains(text(".page:not([hidden])"), " · 0 active") &&
			script(`return [...document.querySelectorAll('#rail button')].some(e=>e.textContent.trim()==='Downloads')`) == true
	})
	expected := 0
	must(filepath.WalkDir(remote, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(remote, path)
		if err != nil {
			return err
		}
		if rel != "sample-0.bin" && rel != "sample-1.bin" && !strings.HasPrefix(rel, "tree"+string(filepath.Separator)) {
			return nil
		}
		expected++
		a, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(filepath.Join(downloads, rel))
		if err != nil {
			return err
		}
		if !bytes.Equal(a, b) {
			return fmt.Errorf("content differs: %s", rel)
		}
		return nil
	}))
	local := 0
	must(filepath.WalkDir(downloads, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			local++
			if strings.HasSuffix(path, ".spiid-part") || strings.HasSuffix(path, ".spiid-state") {
				return fmt.Errorf("leftover: %s", path)
			}
		}
		return nil
	}))
	if expected != 62 || local != 62 {
		t.Fatalf("file counts remote=%d local=%d", expected, local)
	}
	click("Quit Spiid")
	select {
	case err := <-appDone:
		must(err)
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not exit after Quit Spiid")
	}
}
