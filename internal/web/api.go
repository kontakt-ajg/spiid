package web

import (
	"encoding/json"
	"errors"
	"net/http"
)

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/call":
		s.call(w, r)
	case "/api/answer":
		s.answer(w, r)
	case "/api/events":
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		s.events(w, r)
	default:
		if len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/api/" {
			http.NotFound(w, r)
			return
		}
		s.static().ServeHTTP(w, r)
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	err := decodeJSON(r.Body, v)
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			http.Error(w, "body too large", 413)
		} else {
			http.Error(w, "bad JSON", 400)
		}
		return false
	}
	return true
}

func (s *Server) call(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var p struct {
		// Method names the JSON method to call.
		Method *string `json:"method"`
		// Params contains the method parameters.
		Params json.RawMessage `json:"params"`
	}
	if !readJSON(w, r, &p) {
		return
	}
	if p.Method == nil || p.Params == nil || string(p.Params) == "null" {
		http.Error(w, "missing fields", 400)
		return
	}
	if _, ok := methodTable[*p.Method]; ok {
		s.opts.Log.Debug("POST /api/call method %s", *p.Method)
	}
	result, err := s.callMethod(r.Context(), *p.Method, p.Params)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"error": errorJSON(err)})
	} else {
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
	}
	if err == nil && *p.Method == "app.quit" {
		go s.Close()
	}
}

func (s *Server) answer(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var p struct {
		// ID identifies the pending prompt.
		ID *int64 `json:"id"`
		// Answer contains the prompt response.
		Answer json.RawMessage `json:"answer"`
	}
	if !readJSON(w, r, &p) {
		return
	}
	if p.ID == nil || p.Answer == nil {
		http.Error(w, "missing fields", 400)
		return
	}
	s.prompts.answer(*p.ID, string(p.Answer))
	w.WriteHeader(204)
}
