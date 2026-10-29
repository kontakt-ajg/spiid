// Package errs defines the error kinds shared by every layer of the core.
package errs

import (
	"context"
	"errors"
	"strings"
)

// Kind classifies an error.
type Kind int

// The error kinds.
const (
	Other Kind = iota
	NotFound
	PermissionDenied
	HostKeyMismatch
	HostKeyRejected
	AuthFailed // "unable to authenticate"
	PoolClosed
	LocalWrite
	RemoteChanged // "remote file changed; add it again"
	LocalExists   // local file exists and differs; choose Overwrite
	Canceled
	ConnectionLost
	Protocol
	InvalidInput // validation errors (config, arguments)
	Locked       // credentials file is encrypted and not unlocked yet
	WrongMasterPassword
)

var kinds = [...]struct{ name, text string }{
	Other:            {"other", "error"},
	NotFound:         {"not_found", "not found"},
	PermissionDenied: {"permission_denied", "permission denied"},
	HostKeyMismatch: {"host_key_mismatch",
		"host key has changed — possible attack (man-in-the-middle); refusing to connect"},
	HostKeyRejected:     {"host_key_rejected", "host key rejected"},
	AuthFailed:          {"auth_failed", "unable to authenticate"},
	PoolClosed:          {"pool_closed", "pool closed"},
	LocalWrite:          {"local_write", "local write failed"},
	RemoteChanged:       {"remote_changed", "remote file changed; add it again"},
	LocalExists:         {"local_exists", "local file exists and differs"},
	Canceled:            {"canceled", "canceled"},
	ConnectionLost:      {"connection_lost", "connection lost"},
	Protocol:            {"protocol", "protocol error"},
	InvalidInput:        {"invalid_input", "invalid input"},
	Locked:              {"locked", "credentials are locked"},
	WrongMasterPassword: {"wrong_master_password", "wrong master password"},
}

// String returns the snake_case name of the kind, used in JSON.
func (k Kind) String() string {
	if k < 0 || int(k) >= len(kinds) {
		return kinds[Other].name
	}
	return kinds[k].name
}

// Permanent reports whether retrying cannot help; false only for ConnectionLost, Protocol and Other.
func (k Kind) Permanent() bool {
	switch k {
	case ConnectionLost, Protocol, Other:
		return false
	}
	return true
}

// Error is an error with a kind, the operation that failed and the path or server it concerned.
type Error struct {
	message string
	Kind    Kind
	Op      string // e.g. "stat", "dial", "save servers"
	Path    string // remote or local path, or server name; may be empty
	Err     error  // cause; may be nil
}

// Error returns "<op> <path>: <message>".
func (e *Error) Error() string {
	if e.message != "" {
		return e.message
	}
	prefix := strings.TrimSpace(e.Op + " " + e.Path)
	var text string
	if e.Kind == Other && e.Err != nil {
		text = e.Err.Error()
	} else {
		k := e.Kind
		if k < 0 || int(k) >= len(kinds) {
			k = Other
		}
		text = kinds[k].text
		if e.Err != nil {
			text += ": " + e.Err.Error()
		}
	}
	if prefix == "" {
		return text
	}
	return prefix + ": " + text
}

// Unwrap returns the cause.
func (e *Error) Unwrap() error {
	return e.Err
}

// New creates an *Error.
func New(kind Kind, op, path string, err error) *Error {
	return &Error{Kind: kind, Op: op, Path: path, Err: err}
}

// KindOf returns the kind of err: the outermost *Error's kind, Canceled for context.Canceled, else Other.
func KindOf(err error) Kind {
	if err == nil {
		return Other
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	if errors.Is(err, context.Canceled) {
		return Canceled
	}
	return Other
}

// Message creates an error with exact user-facing text and a stable kind.
func Message(kind Kind, message string) *Error {
	return &Error{Kind: kind, message: message}
}
