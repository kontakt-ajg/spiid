package plan

import (
	"path/filepath"
	"testing"
)

func TestSafeName(t *testing.T) {
	for _, name := range []string{
		"", ".", "..", "a:b", "a\\b", "a\b", "a\x00b", "a/b", "...", " ",
		"CON", "nul.txt", "COM1.log", "Lpt9", "PRN", "aux.x", "COM9", "COM0", "lpt0.txt", "COM¹",
		"x.", "x ", "a?b", "a*b", "a|b", "a<b", "a>b", "a\"b", "a\x1fb"} {
		if SafeName(name) {
			t.Errorf("SafeName(%q) = true", name)
		}
	}
	for _, name := range []string{
		".bashrc", "日本語", "ſ", "a.bin", "con-fig.txt", "nullable.go", "COM10", "COMX", "comma.txt",
	} {
		if !SafeName(name) {
			t.Errorf("SafeName(%q) = false", name)
		}
	}
}

func TestLocalPathStaysInside(t *testing.T) {
	dl := t.TempDir()
	for _, components := range [][]string{{"..", "escape"}, {"/absolute"}, {"a/b"}, {"a", ".."}} {
		if local, reason := localPath(dl, components); local != "" || reason != "unsafe name" {
			t.Fatalf("%v: %q %q", components, local, reason)
		}
	}
	if local, reason := localPath(dl, []string{"..hidden"}); local != filepath.Join(dl, "..hidden") || reason != "" {
		t.Fatalf("%q %q", local, reason)
	}
	if local, reason := localPath(dl, []string{"safe", "file"}); local != filepath.Join(dl, "safe", "file") ||
		reason != "" {
		t.Fatalf("%q %q", local, reason)
	}
}

func TestRelCheckRejectsEscape(t *testing.T) {
	dl := t.TempDir()
	for _, tc := range []struct {
		local string
		want  bool
	}{
		{dl + "/../x", false},
		{"/elsewhere/x", false},
		{dl + "/..x", true},
		{dl + "/a/b", true},
	} {
		if got := inside(dl, tc.local); got != tc.want {
			t.Errorf("inside(%q, %q) = %v, want %v", dl, tc.local, got, tc.want)
		}
	}
}
