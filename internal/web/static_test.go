package web

import (
	"io/fs"
	"mime"
	"net/http/httptest"
	"path"
	"regexp"
	"strings"
	"testing"
)

func TestStaticAssets(t *testing.T) {
	assets, err := fs.Sub(files, "static")
	if err != nil {
		t.Fatal(err)
	}
	references := regexp.MustCompile(`(?:src|href)=["']([^"']+)["']`)
	imports := regexp.MustCompile(`(?m)\b(?:import|export)\s+(?:[^;\n]*?\s+from\s+)?["']([^"']+)["']`)
	urls := regexp.MustCompile(`url\(['"]?([^)'"]+)['"]?\)`)
	scripts := regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script\s*>`)
	scriptSource := regexp.MustCompile(`\bsrc\s*=\s*["'][^"']+["']`)
	style := regexp.MustCompile(`(?i)\bstyle\s*=`)
	seen := map[string]bool{}
	checkRef := func(file, ref string) {
		t.Helper()
		target := path.Clean(path.Join(path.Dir(file), ref))
		if strings.HasPrefix(ref, "/") {
			target = strings.TrimPrefix(ref, "/")
		}
		if _, err := fs.ReadFile(assets, target); err != nil {
			t.Errorf("%s references missing embedded %s: %v", file, target, err)
		}
	}
	err = fs.WalkDir(assets, ".", func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		seen[file] = true
		ext := path.Ext(file)
		if ext != ".js" && ext != ".css" && ext != ".html" {
			return nil // Fonts and their verbatim license are not executable assets.
		}
		data, err := fs.ReadFile(assets, file)
		if err != nil {
			return err
		}
		text := string(data)
		for _, forbidden := range []string{"http://", "https://", "innerHTML"} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s contains %s", file, forbidden)
			}
		}
		if style.MatchString(text) {
			t.Errorf("%s contains inline style", file)
		}
		for _, script := range scripts.FindAllStringSubmatch(text, -1) {
			if !scriptSource.MatchString(script[1]) || strings.TrimSpace(script[2]) != "" {
				t.Errorf("%s contains inline script", file)
			}
		}
		var refs [][]string
		switch ext {
		case ".html":
			refs = references.FindAllStringSubmatch(text, -1)
		case ".js":
			refs = imports.FindAllStringSubmatch(text, -1)
		case ".css":
			refs = urls.FindAllStringSubmatch(text, -1)
		}
		for _, ref := range refs {
			checkRef(file, ref[1])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{
		"index.html",
		"fonts/Inter-Regular.ttf",
		"fonts/Inter-Medium.ttf",
		"fonts/Inter-SemiBold.ttf",
		"fonts/Inter-Bold.ttf",
		"fonts/OFL.txt",
	} {
		if !seen[file] {
			t.Errorf("missing %s", file)
		}
	}
	s := &Server{}
	for file := range seen {
		w := httptest.NewRecorder()
		url := "/" + file
		if file == "index.html" {
			url = "/"
		}
		s.static().ServeHTTP(w, httptest.NewRequest("GET", url, nil))
		if w.Code != 200 {
			t.Errorf("%s: status %d", file, w.Code)
			continue
		}
		contentType, _, err := mime.ParseMediaType(w.Header().Get("Content-Type"))
		if err != nil {
			t.Errorf("%s: %v", file, err)
		}
		expected := map[string][]string{
			".html": {"text/html"}, ".css": {"text/css"},
			".js":  {"text/javascript", "application/javascript"},
			".ttf": {"font/ttf", "application/x-font-ttf"}, ".txt": {"text/plain"},
		}[path.Ext(file)]
		valid := false
		for _, value := range expected {
			valid = valid || value == contentType
		}
		if !valid {
			t.Errorf("%s: unexpected content type %q", file, contentType)
		}
	}
}
