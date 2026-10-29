package sshconn

import (
	"io"
	"os"
)

func defaultAgentConn() (io.ReadWriteCloser, error) {
	return os.OpenFile(`\\.\pipe\openssh-ssh-agent`, os.O_RDWR, 0)
}
