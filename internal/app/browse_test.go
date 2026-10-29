package app

import (
	"context"
	"testing"

	"spiid/internal/errs"
)

func TestListUnknownServer(t *testing.T) {
	e := newEnv(t, sftpOpts())
	s := e.service()
	if _, err := s.List(context.Background(), "nope", ""); errs.KindOf(err) != errs.InvalidInput {
		t.Fatalf("got %v", err)
	}
}
