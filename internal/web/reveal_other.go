//go:build !windows

package web

import "os/exec"

func windowsReveal(path string) *exec.Cmd { return exec.Command("explorer", "/select,"+path) }
