// Package logx provides the terminal logger shared by the front ends and core.
package logx

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

type Logger struct {
	mu      sync.Mutex
	w       io.Writer
	verbose bool
}

func New(w io.Writer, verbose bool) *Logger { return &Logger{w: w, verbose: verbose} }

func (l *Logger) write(level, format string, args ...any) {
	if l == nil || (level == "DEBUG" && !l.verbose) {
		return
	}
	message := fmt.Sprintf(format, args...)
	message = strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return ' '
		}
		return r
	}, message)
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, "%s  %s  %s\n", time.Now().Format("15:04:05"), level, message)
}

func (l *Logger) Info(f string, a ...any) { l.write("INFO", f, a...) }

func (l *Logger) Warn(f string, a ...any) { l.write("WARN", f, a...) }

func (l *Logger) Error(f string, a ...any) { l.write("ERROR", f, a...) }

func (l *Logger) Debug(f string, a ...any) { l.write("DEBUG", f, a...) }
