package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"spiid/internal/app"
)

func setup(t *testing.T, modify func(*Options)) (*Server, *Server) {
	t.Helper()
	home := t.TempDir()
	opts := Options{App: app.Options{Home: home, ConfigDir: home, KnownHosts: home + "/known_hosts"}}
	if modify != nil {
		modify(&opts)
	}
	s, err := Start(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, s
}

func request(t *testing.T, s *Server, method, path, body string, auth bool, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+s.host+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{
		Timeout:       3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Header.Values("Set-"+"Cookie")) != 0 {
		t.Fatal(resp.Header)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func post(t *testing.T, s *Server, path, body string) *http.Response {
	return request(t, s, "POST", path, body, true,
		map[string]string{"Origin": "http://" + s.host, "Content-Type": "application/json"})
}

func TestHostCheck(t *testing.T) {
	s, _ := setup(t, nil)
	for _, path := range []string{
		"/",
		"/index.html",
		"/api/missing",
		"/api/call",
		"/api/answer",
		"/api/events",
		"/missing",
	} {
		req := httptest.NewRequest("GET", "http://evil"+path, nil)
		req.Host = "evil"
		req.Header.Set("Authorization", "Bearer "+s.token)
		w := httptest.NewRecorder()
		s.http.Handler.ServeHTTP(w, req)
		if w.Code != 403 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
}

func TestTokenFlow(t *testing.T) {
	s, _ := setup(t, nil)
	for _, tc := range []struct {
		path, token string
		status      int
	}{
		{"/", "", 200}, {"/index.html", "", 200},
		{"/api/call", "", 401}, {"/api/call", "wrong", 401},
		{"/api/call", s.token, 200}, {"/api/call?t=" + s.token, "", 401},
	} {
		method := "POST"
		if !strings.HasPrefix(tc.path, "/api/") {
			method = "GET"
		}
		r := request(t, s, method, tc.path, `{"method":"app.info","params":{}}`, false,
			map[string]string{"Authorization": "Bearer " + tc.token, "Origin": "http://" + s.host,
				"Content-Type": "application/json"})
		if r.StatusCode != tc.status {
			t.Fatalf("%s: %s", tc.path, r.Status)
		}
	}
}

func TestPostNeedsOriginAndJSON(t *testing.T) {
	s, _ := setup(t, nil)
	for i, h := range []map[string]string{
		{"Content-Type": "application/json"},
		{"Origin": "http://evil", "Content-Type": "application/json"},
		{"Origin": "http://" + s.host, "Content-Type": "text/plain"},
	} {
		r := request(t, s, "POST", "/api/call", `{"method":"app.info","params":{}}`, true, h)
		want := 403
		if i == 2 {
			want = 415
		}
		if r.StatusCode != want {
			t.Fatal(r.Status)
		}
	}
}

func TestBodyLimit(t *testing.T) {
	s, _ := setup(t, nil)
	for _, path := range []string{"/api/call", "/api/answer"} {
		r := post(t, s, path, strings.Repeat(" ", (1<<20)+1))
		if r.StatusCode != 413 {
			t.Fatal(r.Status)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	s, _ := setup(t, nil)
	for _, path := range []string{"/", "/api/missing", "/api/missing"} {
		r := request(t, s, "GET", path, "", true, nil)
		if r.Header.Get("Content-Security-Policy") != csp || r.Header.Get("X-Content-Type-Options") != "nosniff" ||
			r.Header.Get("Referrer-Policy") != "no-referrer" {
			t.Fatal(r.Header)
		}
		if strings.HasPrefix(path, "/api/") && r.Header.Get("Cache-Control") != "no-store" {
			t.Fatal(r.Header)
		}
	}
}

func TestCallRoundTrip(t *testing.T) {
	s, _ := setup(t, nil)
	for _, m := range []string{"app.info", "servers.list", "unknown"} {
		r := post(t, s, "/api/call", `{"method":"`+m+`","params":{}}`)
		var v map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			t.Fatal(err)
		}
		if r.StatusCode != 200 {
			t.Fatal(r.Status)
		}
		if m == "unknown" {
			if !strings.Contains(string(v["error"]), "invalid_input") {
				t.Fatal(v)
			}
		} else if v["result"] == nil {
			t.Fatal(v)
		}
	}
}

func TestQuitMethod(t *testing.T) {
	s, _ := setup(t, nil)
	r := post(t, s, "/api/call", `{"method":"app.quit","params":{}}`)
	data, err := io.ReadAll(r.Body)
	if err != nil || r.StatusCode != 200 || strings.TrimSpace(string(data)) != `{"result":{}}` {
		t.Fatalf("%s %v", data, err)
	}
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("quit did not stop")
	}
}

func TestNoCORS(t *testing.T) {
	s, _ := setup(t, nil)
	for _, method := range []string{"GET", "OPTIONS", "POST"} {
		r := request(t, s, method, "/api/missing", "{}", true, map[string]string{"Origin": "http://evil"})
		for k := range r.Header {
			if strings.HasPrefix(strings.ToLower(k), "access-control-") {
				t.Fatal(r.Header)
			}
		}
	}
}
