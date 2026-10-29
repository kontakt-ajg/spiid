// Package webdriver provides the W3C commands needed by the Safari integration test.
package webdriver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"time"
)

const elementKey = "element-6066-11e4-a52e-4f735466cecf"

type Element map[string]string

type Client struct {
	ctx           context.Context
	process       *exec.Cmd
	http          *http.Client
	base, session string
}

func Start(ctx context.Context) (*Client, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	c := &Client{
		ctx:  ctx,
		http: &http.Client{Timeout: 30 * time.Second},
		base: fmt.Sprintf("http://127.0.0.1:%d", port),
	}
	c.process = exec.CommandContext(ctx, "safaridriver", "-p", fmt.Sprint(port))
	if err = c.process.Start(); err != nil {
		return nil, err
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		if err = c.call("GET", "/status", nil, nil); err == nil {
			return c, nil
		}
		select {
		case <-ctx.Done():
			c.Close()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	c.Close()
	return nil, fmt.Errorf("safaridriver readiness: %w", err)
}

func (c *Client) call(method, path string, body, out any) error {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(c.ctx, method, c.base+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	r, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	var envelope struct {
		Value json.RawMessage `json:"value"`
	}
	dec := json.NewDecoder(r.Body)
	if err = dec.Decode(&envelope); err != nil {
		return err
	}
	var trailing any
	if err = dec.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("trailing WebDriver response: %v", err)
	}
	if len(envelope.Value) == 0 {
		return fmt.Errorf("missing WebDriver value")
	}
	var failure struct{ Error string }
	_ = json.Unmarshal(envelope.Value, &failure)
	if r.StatusCode >= 300 || failure.Error != "" {
		return fmt.Errorf("WebDriver %s %s: %s", method, path, envelope.Value)
	}
	if out != nil {
		return json.Unmarshal(envelope.Value, out)
	}
	return nil
}

func (c *Client) NewSession() error {
	var s struct {
		SessionID string `json:"sessionId"`
	}
	capabilities := map[string]any{
		"capabilities": map[string]any{
			"alwaysMatch": map[string]string{"browserName": "safari"},
		},
	}
	if err := c.call("POST", "/session", capabilities, &s); err != nil {
		return err
	}
	if s.SessionID == "" {
		return fmt.Errorf("missing sessionId")
	}
	c.session = "/session/" + s.SessionID
	return nil
}

func (c *Client) Navigate(url string) error {
	return c.call("POST", c.session+"/url", map[string]string{"url": url}, nil)
}

func (c *Client) Find(css string) (Element, error) {
	var e Element
	err := c.call("POST", c.session+"/element", map[string]string{"using": "css selector", "value": css}, &e)
	if err == nil && e[elementKey] == "" {
		err = fmt.Errorf("missing element ID")
	}
	return e, err
}

func (c *Client) FindAll(css string) ([]Element, error) {
	var es []Element
	err := c.call("POST", c.session+"/elements", map[string]string{"using": "css selector", "value": css}, &es)
	for _, e := range es {
		if e[elementKey] == "" {
			return nil, fmt.Errorf("missing element ID")
		}
	}
	return es, err
}

func (c *Client) Click(e Element) error {
	return c.call("POST", c.session+"/element/"+e[elementKey]+"/click", map[string]any{}, nil)
}

func (c *Client) SendKeys(e Element, text string) error {
	path := c.session + "/element/" + e[elementKey]
	if err := c.call("POST", path+"/clear", map[string]any{}, nil); err != nil {
		return err
	}
	return c.call("POST", path+"/value", map[string]string{"text": text}, nil)
}

func (c *Client) Text(e Element) (string, error) {
	var text string
	err := c.call("GET", c.session+"/element/"+e[elementKey]+"/text", nil, &text)
	return text, err
}

func (c *Client) ExecuteScript(js string, args ...any) (any, error) {
	if args == nil {
		args = []any{}
	}
	var out any
	err := c.call("POST", c.session+"/execute/sync", map[string]any{"script": js, "args": args}, &out)
	return out, err
}

func (c *Client) Screenshot() ([]byte, error) {
	var s string
	if err := c.call("GET", c.session+"/screenshot", nil, &s); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(s)
}

func (c *Client) Close() {
	if c.session != "" {
		_ = c.call("DELETE", c.session, nil, nil)
		c.session = ""
		_ = exec.Command("killall", "Safari").Run()
	}
	if c.process != nil && c.process.Process != nil {
		_ = c.process.Process.Kill()
		_ = c.process.Wait()
		c.process = nil
	}
}
