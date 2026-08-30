// Package logx gives every run a tagged logger writing to both stderr and file.
package logx

import (
	"fmt"
	"io"
	"log/slog"
	"os"
)

// New returns a logger that tees to stderr and path, plus the file handle to close.
func New(path string, debug bool) (*slog.Logger, io.Closer, error) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("open log %s: %w", path, err)
	}
	h := slog.NewTextHandler(io.MultiWriter(os.Stderr, f), &slog.HandlerOptions{Level: level})
	return slog.New(h), f, nil
}
