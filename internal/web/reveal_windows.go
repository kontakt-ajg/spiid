package web

import (
	"os/exec"
	"syscall"
)

func windowsReveal(path string) *exec.Cmd {
	cmd := exec.Command("explorer")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer /select,"` + path + `"`}
	return cmd
}
