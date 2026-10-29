package errs

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestKindStrings(t *testing.T) {
	want := []string{"other", "not_found", "permission_denied", "host_key_mismatch", "host_key_rejected",
		"auth_failed", "pool_closed", "local_write", "remote_changed", "local_exists", "canceled",
		"connection_lost", "protocol", "invalid_input", "locked", "wrong_master_password"}
	for i, w := range want {
		if got := Kind(i).String(); got != w {
			t.Errorf("Kind(%d) = %q, want %q", i, got, w)
		}
	}
	if len(want) != 16 {
		t.Fatalf("expected 16 kinds, have %d", len(want))
	}
}

func TestErrorText(t *testing.T) {
	cause := errors.New("boom")
	tests := []struct {
		err  *Error
		want string
	}{
		{New(NotFound, "stat", "/a", nil), "stat /a: not found"},
		{New(NotFound, "", "/a", nil), "/a: not found"},
		{New(NotFound, "stat", "", nil), "stat: not found"},
		{New(NotFound, "", "", nil), "not found"},
		{New(LocalWrite, "write", "/x", cause), "write /x: local write failed: boom"},
		{New(Other, "dial", "srv", cause), "dial srv: boom"},
		{New(Other, "", "", cause), "boom"},
		{New(Other, "dial", "", nil), "dial: error"},
	}
	for _, tt := range tests {
		if got := tt.err.Error(); got != tt.want {
			t.Errorf("got %q, want %q", got, tt.want)
		}
	}
}

func TestPermanent(t *testing.T) {
	for k := Other; k <= WrongMasterPassword; k++ {
		want := k != ConnectionLost && k != Protocol && k != Other
		if got := k.Permanent(); got != want {
			t.Errorf("%v.Permanent() = %v, want %v", k, got, want)
		}
	}
}

func TestKindOf(t *testing.T) {
	inner := New(NotFound, "stat", "/a", nil)
	outer := New(LocalWrite, "write", "/b", inner)
	tests := []struct {
		name string
		err  error
		want Kind
	}{
		{"nil", nil, Other},
		{"direct", inner, NotFound},
		{"wrapped", fmt.Errorf("ctx: %w", inner), NotFound},
		{"outermost wins", outer, LocalWrite},
		{"canceled", context.Canceled, Canceled},
		{"wrapped canceled", fmt.Errorf("x: %w", context.Canceled), Canceled},
		{"plain", errors.New("x"), Other},
	}
	for _, tt := range tests {
		if got := KindOf(tt.err); got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}
