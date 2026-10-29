//go:build !windows

package engine

import (
	"os"
	"syscall"
)

var finishedMode os.FileMode

func init() {
	mask := syscall.Umask(0)
	syscall.Umask(mask)
	finishedMode = os.FileMode(0o666 &^ mask)
}
