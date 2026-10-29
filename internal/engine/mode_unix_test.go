//go:build !windows

package engine

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestStartupUmask(t *testing.T) {
	if os.Getenv("SPIID_TEST_UMASK") != "1" {
		cmd := exec.Command("/bin/sh", "-c", `umask 077; exec "$1" -test.run=^TestStartupUmask$`, "sh", os.Args[0])
		cmd.Env = append(os.Environ(), "SPIID_TEST_UMASK=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return
	}
	if finishedMode != 0600 {
		t.Fatalf("startup mode %o", finishedMode)
	}
	dir := t.TempDir()
	f, err := openPart(dir+"/part", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := finishPart(f, dir+"/part", dir+"/file", time.Now(), false); err != nil {
		t.Fatal(err)
	}
	j := &job{req: Request{Local: dir + "/empty"}, mtime: time.Now()}
	if err := j.finishEmpty(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir + "/file", dir + "/empty"} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("%s: %v %v", path, info, err)
		}
	}
}
