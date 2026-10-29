package web

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestMasterMethods(t *testing.T) {
	s, ch := testMethods(t)
	for _, password := range []string{"", "1234567"} {
		params := json.RawMessage(marshal(masterParams{ptr(password)}))
		_, err := s.callMethod(context.Background(), "credentials.protect", params)
		if err == nil || errorJSON(err).Kind != "invalid_input" {
			t.Fatalf("short: %v", err)
		}
	}
	call(t, s, ch, "credentials.protect", masterParams{ptr("first-master")})
	for method, params := range map[string]any{
		"credentials.change":    changeMasterParams{ptr("wrong"), ptr("second-master")},
		"credentials.unprotect": currentMasterParams{ptr("wrong")},
	} {
		_, err := s.callMethod(context.Background(), method, json.RawMessage(marshal(params)))
		if err == nil || errorJSON(err).Kind != "wrong_master_password" ||
			!strings.EqualFold(errorJSON(err).Message, "wrong master password") {
			t.Fatalf("wrong current: %v", err)
		}
	}
	call(t, s, ch, "credentials.change", changeMasterParams{ptr("first-master"), ptr("second-master")})
	call(t, s, ch, "credentials.unprotect", currentMasterParams{ptr("second-master")})
	call(t, s, ch, "credentials.protect", masterParams{ptr("third-master")})
	call(t, s, ch, "credentials.reset", empty{})
	var status struct {
		Encrypted bool `json:"encrypted"`
	}
	json.Unmarshal(call(t, s, ch, "credentials.status", empty{}), &status)
	if status.Encrypted {
		t.Fatal("reset stayed encrypted")
	}
	_, err := s.callMethod(context.Background(), "settings.save", json.RawMessage(marshal(map[string]any{
		"download_dir": "relative", "threads_per_file": 4, "parallel_files": 3, "max_connections": 8, "theme": "system",
	})))
	if err == nil || errorJSON(err).Kind != "invalid_input" {
		t.Fatalf("relative directory: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }
