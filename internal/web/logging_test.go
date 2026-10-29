package web

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"spiid/internal/logx"
)

func TestForbiddenLogging(t *testing.T) {
	var b bytes.Buffer
	s, _ := setup(t, func(o *Options) { o.Log = logx.New(&b, true); o.App.Log = o.Log })
	r := request(t, s, http.MethodGet, "/api/events?t=private", "", false, nil)
	io.Copy(io.Discard, r.Body)
	r.Body.Close()
	post(t, s, "/api/call", `{"method":"app.info","params":{}}`).Body.Close()
	resp, _ := connect(t, s)
	resp.Body.Close()
	s.Close()
	if !strings.Contains(b.String(), "WARN  HTTP 401: token") ||
		strings.Contains(b.String(), "private") || strings.Contains(b.String(), s.token) {
		t.Fatal(b.String())
	}
}
