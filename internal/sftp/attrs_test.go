package sftp

import (
	"errors"
	"io"
	"io/fs"
	"reflect"
	"testing"
	"time"

	"spiid/internal/errs"
)

func TestAttrsRoundTrip(t *testing.T) {
	flagBits := []uint32{attrSize, attrUIDGID, attrPermissions, attrACModTime, attrExtended}
	for mask := 0; mask < 1<<len(flagBits); mask++ {
		var a fixtureAttrs
		for i, f := range flagBits {
			if mask&(1<<i) != 0 {
				a.flags |= f
			}
		}
		if a.flags&attrSize != 0 {
			a.size = 1<<40 + 12345
		}
		if a.flags&attrUIDGID != 0 {
			a.uid, a.gid = 501, 20
		}
		if a.flags&attrPermissions != 0 {
			a.perm = 0o100644
		}
		if a.flags&attrACModTime != 0 {
			a.atime, a.mtime = 1700000000, 1700000123
		}
		if a.flags&attrExtended != 0 {
			a.extensions = []extPair{{"a@b", "xyz"}, {"c@d", ""}}
		}
		wire := a.appendTo(nil)
		d := &decoder{b: wire}
		got := parseAttrs(d)
		if d.err != nil || len(d.b) != 0 {
			t.Fatalf("flags %#x: err %v, %d bytes left", a.flags, d.err, len(d.b))
		}
		if !reflect.DeepEqual(got, attrs{flags: a.flags, size: a.size, perm: a.perm, mtime: a.mtime}) {
			t.Fatalf("flags %#x: got %+v, want %+v", a.flags, got, a)
		}
		fi, err := got.fileInfo("n")
		if err != nil {
			t.Fatal(err)
		}
		if a.flags&attrSize != 0 && fi.Size != 1<<40+12345 {
			t.Fatalf("size %d", fi.Size)
		}
		if a.flags&attrACModTime != 0 && !fi.ModTime.Equal(time.Unix(1700000123, 0)) {
			t.Fatalf("mtime %v", fi.ModTime)
		}
		if a.flags&attrACModTime == 0 && !fi.ModTime.IsZero() {
			t.Fatalf("mtime should be zero, got %v", fi.ModTime)
		}
		if a.flags&attrPermissions == 0 && fi.Mode != fs.ModeIrregular {
			t.Fatalf("no permissions: mode %v, want ModeIrregular", fi.Mode)
		}
		if a.flags&attrPermissions != 0 && (fi.Mode != 0o644 || !fi.IsRegular()) {
			t.Fatalf("mode %v", fi.Mode)
		}
		if a.flags&attrSize == 0 && fi.Size != 0 {
			t.Fatalf("size without flag: %d", fi.Size)
		}
	}
}

func TestAttrsTruncated(t *testing.T) {
	a := fixtureAttrs{flags: attrSize | attrUIDGID | attrPermissions | attrACModTime | attrExtended,
		size: 5, perm: 0o100600, mtime: 1, extensions: []extPair{{"type", "data"}}}
	wire := a.appendTo(nil)
	for n := 0; n < len(wire); n++ {
		d := &decoder{b: wire[:n]}
		parseAttrs(d)
		if d.err == nil {
			t.Fatalf("truncated at %d: no error", n)
		}
	}
}

func TestPermissionTypeBits(t *testing.T) {
	cases := []struct {
		perm uint32
		want fs.FileMode
	}{
		{0o040755, fs.ModeDir | 0o755},
		{0o100644, 0o644},
		{0o120777, fs.ModeSymlink | 0o777},
		{0o010600, fs.ModeNamedPipe | 0o600},
		{0o140600, fs.ModeSocket | 0o600},
		{0o020620, fs.ModeDevice | fs.ModeCharDevice | 0o620},
		{0o060660, fs.ModeDevice | 0o660},
		{0o170000, fs.ModeIrregular},
		{0o004755, fs.ModeIrregular | 0o755},
	}
	for _, c := range cases {
		a := attrs{flags: attrPermissions, perm: c.perm}
		fi, err := a.fileInfo("x")
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode != c.want {
			t.Errorf("perm %#o: mode %v, want %v", c.perm, fi.Mode, c.want)
		}
	}
	dir, _ := attrs{flags: attrPermissions, perm: 0o040755}.fileInfo("d")
	link, _ := attrs{flags: attrPermissions, perm: 0o120777}.fileInfo("l")
	if !dir.IsDir() || dir.IsRegular() || !link.IsSymlink() || link.IsRegular() {
		t.Fatal("IsDir/IsRegular/IsSymlink wrong")
	}
}

// fakeServer answers INIT with VERSION 3, then runs fn on the connection.
func fakeServer(t *testing.T, fn func(r io.Reader, w io.Writer)) (io.Reader, io.WriteCloser) {
	t.Helper()
	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	go func() {
		defer s2cW.Close()
		defer c2sR.Close()
		_, _, err := readPacket(c2sR)
		if err != nil {
			return
		}
		s2cW.Write([]byte{0, 0, 0, 5, fxpVersion, 0, 0, 0, 3})
		fn(c2sR, s2cW)
	}()
	return s2cR, c2sW
}

func causeKinds(err error) []errs.Kind {
	var kinds []errs.Kind
	for ; err != nil; err = errors.Unwrap(err) {
		if e, ok := err.(*errs.Error); ok {
			kinds = append(kinds, e.Kind)
		}
	}
	return kinds
}

func hasKind(err error, k errs.Kind) bool {
	for _, got := range causeKinds(err) {
		if got == k {
			return true
		}
	}
	return false
}
