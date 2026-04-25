// auditlog provides a separate slog.Logger for audit events that
// fan-outs to stdout AND (optionally) an append-only file.
//
// Why a separate logger and not just slog.Default()? Because audit
// records have a different durability contract from operational logs:
// they should survive Postgres compromise. Routing them through their
// own writer set lets us guarantee the file mirror happens regardless
// of how operational logging is later reconfigured.

package auditlog

import (
	"errors"
	"io"
	"log/slog"
	"os"
)

// Open returns a slog.Logger that writes JSON to stdout and (if path is
// non-empty) to the file at path in append-mode. The returned closer
// must be invoked at shutdown to flush + close the file.
func Open(path string) (*slog.Logger, io.Closer, error) {
	writers := []io.Writer{os.Stdout}
	var f *os.File
	if path != "" {
		// O_APPEND is atomic on POSIX for writes < PIPE_BUF; we always
		// emit one JSON line per call (well under that limit) so
		// concurrent broker goroutines do not interleave records.
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

var _ = errors.New
