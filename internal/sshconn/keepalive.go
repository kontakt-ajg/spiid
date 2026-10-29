package sshconn

import (
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"

	"spiid/internal/logx"
	"spiid/internal/sftp"
)

// Conn is one SSH connection with an SFTP client.
type Conn struct {
	server string
	log    *logx.Logger
	SFTP   *sftp.Client

	client   *ssh.Client
	stop     chan struct{}
	stopOnce sync.Once
	returned atomic.Bool // set by the first Put/Discard
}

func (m *Manager) newConn(s *sftp.Client, client *ssh.Client, server string) *Conn {
	c := &Conn{log: m.opts.Log, server: server, SFTP: s, client: client, stop: make(chan struct{})}
	go c.keepalive(m.opts.Keepalive, m.opts.KeepaliveMiss)
	return c
}

func (c *Conn) dead() bool {
	select {
	case <-c.SFTP.Done():
		return true
	default:
		return false
	}
}

func (c *Conn) close() {
	c.stopOnce.Do(func() {
		c.log.Info("ssh: disconnected from %s", c.server)
		close(c.stop)
		c.client.Close()
		c.SFTP.Close()
	})
}

// keepalive pings every interval; after miss consecutive unanswered pings it closes the connection.
func (c *Conn) keepalive(interval time.Duration, miss int) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	misses := 0
	for {
		select {
		case <-c.stop:
			return
		case <-c.SFTP.Done():
			c.close()
			return
		case <-tick.C:
		}
		reply := make(chan error, 1)
		go func() {
			_, _, err := c.client.SendRequest("keepalive@openssh.com", true, nil)
			reply <- err
		}()
		timer := time.NewTimer(interval)
		select {
		case err := <-reply:
			timer.Stop()
			if err == nil {
				misses = 0
			} else {
				misses++
			}
		case <-timer.C:
			misses++
		case <-c.stop:
			timer.Stop()
			return
		}
		if misses >= miss {
			c.log.Warn("ssh: dead connection to %s (keepalive)", c.server)
			c.close()
			return
		}
	}
}
