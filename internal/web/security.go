package web

import (
	"crypto/subtle"
	"mime"
	"net/http"
	"strings"
	"time"
)

const csp = "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; frame-ancestors 'none'"

func (s *Server) valid(token string) bool {
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) == 1
}

func (s *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseLog{ResponseWriter: w, status: 200}
		w = rw
		defer func() { s.opts.Log.Debug("%s %s %d %s", r.Method, r.URL.Path, rw.status, time.Since(start)) }()
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		if r.Host != s.host {
			s.opts.Log.Warn("HTTP 403: host")
			http.Error(w, "forbidden", 403)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			token := ""
			if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				token = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			}
			if r.Method == "GET" && r.URL.Path == "/api/events" && !s.valid(token) {
				token = r.URL.Query().Get("t")
			}
			if !s.valid(token) {
				s.opts.Log.Warn("HTTP 401: token")
				http.Error(w, "unauthorized", 401)
				return
			}
		}
		if r.Method == "POST" {
			if r.Header.Get("Origin") != "http://"+s.host {
				s.opts.Log.Warn("HTTP 403: origin")
				http.Error(w, "forbidden", 403)
				return
			}
			media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || media != "application/json" {
				http.Error(w, "JSON required", 415)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		}
		next.ServeHTTP(w, r)
	})
}

type responseLog struct {
	http.ResponseWriter
	status int
}

// WriteHeader records the response status before forwarding it.
func (w *responseLog) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// Flush flushes the underlying HTTP response.
func (w *responseLog) Flush() { http.NewResponseController(w.ResponseWriter).Flush() }

// Unwrap exposes the underlying writer to HTTP response controllers.
func (w *responseLog) Unwrap() http.ResponseWriter { return w.ResponseWriter }
