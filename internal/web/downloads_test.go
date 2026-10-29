package web

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDownloadsState(t *testing.T) {
	b, ch := testMethods(t)
	raw := call(t, b, ch, "downloads.state", sinceParams{ptr(0)})
	var state map[string]json.RawMessage
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"jobs", "summary", "scans", "messages", "next"} {
		if _, ok := state[key]; !ok {
			t.Fatalf("missing %s: %s", key, raw)
		}
	}
	call(t, b, ch, "downloads.clear_finished", empty{})
	if _, err := b.callMethod(context.Background(), "downloads.state", json.RawMessage(`{}`)); err == nil {
		t.Fatal("accepted missing since")
	}
}
