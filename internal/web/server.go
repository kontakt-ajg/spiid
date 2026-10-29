// Package web serves the local browser UI, JSON methods, and user prompts over authenticated HTTP.
package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"spiid/internal/app"
	"spiid/internal/logx"
)

// Options configures the local HTTP server and its application.
type Options struct {
	// App configures the application created by Start.
	App app.Options
	// Port selects a loopback port; zero requests an available port.
	Port int
	// Log receives terminal messages; nil is silent.
	Log *logx.Logger
}

// Server owns the HTTP listener, event streams, prompts, and application service.
type Server struct {
	opts     Options
	http     *http.Server
	listener net.Listener
	token    string
	host     string
	done     chan struct{}
	once     sync.Once
	mu       sync.Mutex
	streams  map[*stream]bool
	closing  bool
	service  *app.Service
	prompts  *prompter
	ctx      context.Context
	cancel   context.CancelFunc
}

// Start creates the application and starts listening on IPv4 loopback.
func Start(opts Options) (*Server, error) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		opts:    opts,
		token:   hex.EncodeToString(token),
		done:    make(chan struct{}),
		streams: make(map[*stream]bool),
		ctx:     ctx,
		cancel:  cancel,
	}

	s.prompts = newPrompter(ctx, s.broadcast)
	s.prompts.log = opts.Log
	opts.App.Prompter = s.prompts
	service, err := app.New(opts.App)
	if err != nil {
		cancel()
		return nil, err
	}
	s.service = service
	l, err := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(opts.Port))
	if err != nil {
		s.prompts.stop()
		cancel()
		service.Close()
		return nil, err
	}
	s.listener, s.host = l, l.Addr().String()
	s.http = &http.Server{Handler: s.security(http.HandlerFunc(s.route)), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = s.http.Serve(l); s.Close() }()
	return s, nil
}

// URL returns the browser login URL, including its fragment token.
func (s *Server) URL() string { return "http://" + s.host + "/#t=" + s.token }

// Port returns the port used by the listener.
func (s *Server) Port() int {
	_, port, _ := net.SplitHostPort(s.host)
	n, _ := strconv.Atoi(port)
	return n
}

// Service returns the application owned by this server.
func (s *Server) Service() *app.Service { return s.service }

// Done is closed after the server and application have stopped.
func (s *Server) Done() <-chan struct{} { return s.done }

// Close cancels prompts, ends streams, stops HTTP, and closes the application once.
func (s *Server) Close() error {
	s.once.Do(func() {
		s.prompts.stop()
		s.cancel()
		s.mu.Lock()
		s.closing = true
		for st := range s.streams {
			close(st.done)
			delete(s.streams, st)
		}
		s.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.http.Shutdown(ctx)
		_ = s.http.Close()
		s.service.Close()
		close(s.done)
	})
	return nil
}
