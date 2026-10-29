package plan

import (
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// SafeName reports whether name is a safe, single path component.
func SafeName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.HasSuffix(name, ".") ||
		strings.HasSuffix(name, " ") || strings.ContainsAny(name, "<>:\"/\\|?*") {
		return false
	}
	for _, r := range name {
		if r < 32 {
			return false
		}
	}
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return false
	}
	// Windows reserves COM0–COM9 and LPT0–LPT9, also with the superscript digits ¹ ² ³.
	for _, prefix := range []string{"COM", "LPT"} {
		if rest, ok := strings.CutPrefix(base, prefix); ok && utf8.RuneCountInString(rest) == 1 &&
			strings.ContainsAny(rest, "0123456789¹²³") {
			return false
		}
	}
	return true
}

func localPath(downloadDir string, components []string) (string, string) {
	for _, component := range components {
		if !SafeName(component) {
			return "", "unsafe name"
		}
	}
	local := filepath.Join(append([]string{downloadDir}, components...)...)
	if !inside(downloadDir, local) {
		return "", "outside the download folder"
	}
	return local, ""
}

func inside(downloadDir, local string) bool {
	rel, err := filepath.Rel(downloadDir, local)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	return true
}

func foldingKey(local string) string { return strings.ToLower(strings.ToUpper(local)) }
