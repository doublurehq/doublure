package server

import (
	"context"
	"log/slog"
	"sync"
)

// LogCapture is a minimal slog.Handler retaining every record. It is exported
// from a test file so both the internal and the external (server_test) tests
// share it.
type LogCapture struct {
	mu      sync.Mutex
	entries []slog.Record
}

func (h *LogCapture) Enabled(context.Context, slog.Level) bool { return true }

func (h *LogCapture) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries = append(h.entries, r.Clone())
	return nil
}

func (h *LogCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *LogCapture) WithGroup(string) slog.Handler      { return h }

// Records returns a snapshot of the records captured so far.
func (h *LogCapture) Records() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]slog.Record(nil), h.entries...)
}
