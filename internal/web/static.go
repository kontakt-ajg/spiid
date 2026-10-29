package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var files embed.FS

func (s *Server) static() http.Handler {
	f, _ := fs.Sub(files, "static")
	handler := http.FileServer(http.FS(f))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.html" {
			copy := r.Clone(r.Context())
			copy.URL.Path = "/"
			r = copy
		}
		handler.ServeHTTP(w, r)
	})
}
