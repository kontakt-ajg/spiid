package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"spiid/internal/errs"
)

// DefaultDir returns the config folder for home: %AppData%\spiid on Windows, else $XDG_CONFIG_HOME/spiid (if
// absolute) or <home>/.config/spiid. It creates nothing.
func DefaultDir(home string) (string, error) {
	if runtime.GOOS == "windows" {
		appData := os.Getenv("AppData")
		if appData == "" {
			return "", errs.New(errs.Other, "config dir", "", errors.New("AppData is not set"))
		}
		return appData + `\spiid`, nil
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "spiid"), nil
	}
	return filepath.Join(home, ".config", "spiid"), nil
}
