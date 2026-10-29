package web

import (
	"fmt"
	"net/http"
	"time"
)

type stream struct {
	events chan string
	done   chan struct{}
}

func (s *Server) broadcast(event string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for st := range s.streams {
		select {
		case st.events <- event:
		default:
			close(st.done)
			delete(s.streams, st)
		}
	}
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unavailable", 500)
		return
	}
	st := &stream{events: make(chan string, 64), done: make(chan struct{})}
	registered := false
	pending := s.snapshotPrompts(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.closing {
			s.streams[st] = true
			registered = true
		}
	})
	if !registered {
		http.Error(w, "server closing", 503)
		return
	}
	defer func() { s.mu.Lock(); delete(s.streams, st); s.mu.Unlock() }()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Connection", "keep-alive")
	rc := http.NewResponseController(w)
	write := func(text string) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, err := fmt.Fprint(w, text)
		if err == nil {
			f.Flush()
		}
		return err == nil
	}
	if !write(": connected\n\n") {
		return
	}
	for _, event := range pending {
		if !write("data: " + event + "\n\n") {
			return
		}
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-st.done:
			return
		case event := <-st.events:
			if !write("data: " + event + "\n\n") {
				return
			}
		case <-ticker.C:
			if !write(": ping\n\n") {
				return
			}
		}
	}
}
