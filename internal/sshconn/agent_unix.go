//go:build !windows

package sshconn

import (
	"errors"
	"io"
	"net"
	"os"
)

func defaultAgentConn() (io.ReadWriteCloser, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, errors.New("SSH_AUTH_SOCK is not set")
	}
	return net.Dial("unix", sock)
}
