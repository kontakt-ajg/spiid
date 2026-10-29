package web

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"spiid/internal/errs"
)

func TestLocalList(t *testing.T) {
	b, _ := testMethods(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "z-dir"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a-file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	v, err := b.callMethod(context.Background(), "local.list", json.RawMessage(marshal(dirParams{ptr(dir)})))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Path, Parent string
		Entries      []struct {
			Name, Path string
			IsDir      bool `json:"is_dir"`
		}
	}
	if err = json.Unmarshal([]byte(marshal(v)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Path != dir || got.Parent != filepath.Dir(dir) || len(got.Entries) != 2 || got.Entries[0].Name != "z-dir" ||
		!got.Entries[0].IsDir ||
		got.Entries[1].IsDir ||
		got.Entries[1].Path != filepath.Join(dir, "a-file") {
		t.Fatal(marshal(got))
	}
}

func TestLocalReveal(t *testing.T) {
	b, _ := testMethods(t)
	old := reveal
	defer func() { reveal = old }()
	var got string
	reveal = func(path string) error { got = path; return nil }
	_, err := b.callMethod(context.Background(), "local.reveal", json.RawMessage(`{"path":"/some file"}`))
	if err != nil || got != "/some file" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestLocalListRecognizesSymlinkedDirectory(t *testing.T) {
	b, _ := testMethods(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "real"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	value, err := b.callMethod(context.Background(), "local.list", json.RawMessage(marshal(dirParams{ptr(dir)})))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Entries []struct {
			Name  string
			IsDir bool `json:"is_dir"`
		}
	}
	if err = json.Unmarshal([]byte(marshal(value)), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 2 {
		t.Fatal(marshal(value))
	}
	for _, e := range got.Entries {
		if !e.IsDir {
			t.Fatalf("%s is not listed as a directory", e.Name)
		}
	}
}

func TestLocalRevealPassesPathAsSingleArgument(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake for Unix file-manager commands")
	}
	bin := t.TempDir()
	out := filepath.Join(bin, "argv")
	script := []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$SPIID_REVEAL_ARGV\"\n")
	for _, name := range []string{"open", "xdg-open"} {
		if err := os.WriteFile(filepath.Join(bin, name), script, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SPIID_REVEAL_ARGV", out)
	b, _ := testMethods(t)
	_, err := b.callMethod(context.Background(), "local.reveal", json.RawMessage(`{"path":"-a"}`))
	if errs.KindOf(err) != errs.InvalidInput {
		t.Fatalf("relative path: %v", err)
	}
	if _, err = os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("relative path reached file manager")
	}
	path := filepath.Join(bin, "a file")
	if err = os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = b.callMethod(context.Background(), "local.reveal", json.RawMessage(marshal(pathParams{ptr(path)})))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	want := bin + "\n"
	if runtime.GOOS == "darwin" {
		want = "-R\n--\n" + path + "\n"
	}
	if string(data) != want {
		t.Fatalf("argv %q, want %q", data, want)
	}
}
