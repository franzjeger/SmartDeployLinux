// auditlog provides a separate slog.Logger for audit events that
// fan-outs to stdout AND (optionally) an append-only file. See the
// auth-broker's auditlog package — same contract.

package auditlog

import (
	"io"
	"log/slog"
	"os"
)

func Open(path string) (*slog.Logger, io.Closer, error) {
	writers := []io.Writer{os.Stdout}
	var f *os.File
	if path != "" {
		ff, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0640)
		if err != nil {
			return nil, nil, err
		}
		f = ff
		writers = append(writers, f)
	}
	w := io.MultiWriter(writers...)
	logger := slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	return logger, closerFunc(func() error {
		if f != nil {
			return f.Close()
		}
		return nil
	}), nil
}

type closerFunc func() error

func (c closerFunc) Close() error { return c() }
