package engine

import (
	"context"
	"time"

	"spiid/internal/config"
	"spiid/internal/errs"
	"spiid/internal/logx"
	"spiid/internal/sshconn"
)

// State is the life-cycle state of a job.
type State int

// The job states.
const (
	Queued State = iota
	Running
	Done
	Skipped
	Failed
	Canceled
)

var stateNames = [...]string{"queued", "running", "done", "skipped", "failed", "canceled"}

func (s State) String() string {
	if s < 0 || int(s) >= len(stateNames) {
		return "unknown"
	}
	return stateNames[s]
}

// PoolSource gives the connection pool of a server.
type PoolSource interface {
	Pool(srv config.Server, maxConns int) Pool
}

// Pool hands out connections; every Get is followed by exactly one Put or Discard.
type Pool interface {
	Get(ctx context.Context) (*sshconn.Conn, error)
	Put(c *sshconn.Conn)
	Discard(c *sshconn.Conn)
}

// Request is one download to add.
type Request struct {
	Server    config.Server
	Remote    string
	Local     string
	Overwrite bool
	Root      string // the download folder: ancestors below it must not be symbolic links (empty = skip)
}

// Job is a snapshot of one download.
type Job struct {
	ID             int64
	Server         string
	Remote, Local  string
	State          State
	Size, Done     int64
	Speed          float64 // bytes/s, EWMA
	Segments       int
	ActiveSegments int
	Err            string
	ErrKind        errs.Kind
	Added          time.Time
}

// Options configures a Manager.
type Options struct {
	Log            *logx.Logger
	Pools          PoolSource
	ThreadsPerFile int
	ParallelFiles  int
	MaxConnections int
	MinSegment     int64               // 0 -> 1 MiB
	RetryWaits     []time.Duration     // nil -> 1 s, 2 s, 4 s
	SaveInterval   time.Duration       // 0 -> 1 s
	SpeedInterval  time.Duration       // 0 -> 500 ms
	OnRetry        func(server string) // called when the user retries
}
