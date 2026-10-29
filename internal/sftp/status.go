package sftp

import (
	"errors"
	"fmt"

	"spiid/internal/errs"
)

// parseStatus decodes a STATUS body. Old servers may omit the message and language; a present but truncated
// string, or trailing bytes, is an error.
func parseStatus(body []byte) (code uint32, msg string, err error) {
	d := &decoder{b: body}
	code = d.u32()
	if d.err == nil && len(d.b) > 0 {
		msg = d.str()
	}
	if d.err == nil && len(d.b) > 0 {
		d.str() // language
	}
	if d.err != nil {
		return 0, "", d.err
	}
	if len(d.b) != 0 {
		return 0, "", errors.New("trailing STATUS bytes")
	}
	return code, msg, nil
}

// statusError maps a non-OK STATUS to an error.
func statusError(op, path string, code uint32, msg string) error {
	switch code {
	case statusNoSuchFile:
		return errs.New(errs.NotFound, op, path, nil)
	case statusPermDenied:
		return errs.New(errs.PermissionDenied, op, path, nil)
	case statusOpUnsupported:
		return errs.New(errs.Other, op, path, errors.New("operation unsupported"))
	}
	if msg == "" {
		msg = fmt.Sprintf("status code %d", code)
	}
	return errs.New(errs.Other, op, path, errors.New(msg))
}

// replyError turns a reply that is not the expected type into an error, and marks the client broken when the
// reply makes no sense.
func (c *Client) replyError(op, path string, rep reply) error {
	if rep.typ != fxpStatus {
		return c.badReply(op, path, fmt.Errorf("unexpected reply type %d", rep.typ))
	}
	code, msg, err := parseStatus(rep.body)
	if err != nil {
		return c.badReply(op, path, err)
	}
	if code == statusOK {
		e := errs.New(errs.Protocol, op, path, errors.New("unexpected status OK"))
		c.fail(e)
		return e
	}
	return statusError(op, path, code, msg)
}

// badReply reports a reply that cannot be decoded: the client is broken and the caller gets the same
// errs.ConnectionLost as every other pending and later request.
func (c *Client) badReply(op, path string, cause error) error {
	c.fail(errs.New(errs.Protocol, op, path, cause))
	return c.brokenErr()
}

func isEOF(rep reply) bool {
	if rep.typ != fxpStatus {
		return false
	}
	code, _, err := parseStatus(rep.body)
	return err == nil && code == statusEOF
}
