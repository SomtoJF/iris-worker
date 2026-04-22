package common

import (
	"context"
	"fmt"
	"log/slog"

	"go.temporal.io/sdk/log"
)

// TemporalSlogLogger adapts a slog.Logger to Temporal's log.Logger interface.
// This lets workflow/activity logs flow into the same slog pipeline (handlers, etc).
type TemporalSlogLogger struct {
	logger *slog.Logger
	prefix []any
	// callerSkip is kept to satisfy log.WithSkipCallers; slog doesn't support
	// per-record caller skip, so this is currently a no-op.
	callerSkip int
}

var _ log.Logger = (*TemporalSlogLogger)(nil)
var _ log.WithLogger = (*TemporalSlogLogger)(nil)
var _ log.WithSkipCallers = (*TemporalSlogLogger)(nil)

func NewTemporalSlogLogger(l *slog.Logger) *TemporalSlogLogger {
	return &TemporalSlogLogger{logger: l}
}

func (l *TemporalSlogLogger) With(keyvals ...interface{}) log.Logger {
	cp := *l
	cp.prefix = append(append([]any(nil), l.prefix...), normalizeKeyvals(keyvals)...)
	return &cp
}

func (l *TemporalSlogLogger) WithCallerSkip(skip int) log.Logger {
	cp := *l
	cp.callerSkip += skip
	return &cp
}

func (l *TemporalSlogLogger) Debug(msg string, keyvals ...interface{}) { l.log(slog.LevelDebug, msg, keyvals...) }
func (l *TemporalSlogLogger) Info(msg string, keyvals ...interface{})  { l.log(slog.LevelInfo, msg, keyvals...) }
func (l *TemporalSlogLogger) Warn(msg string, keyvals ...interface{})  { l.log(slog.LevelWarn, msg, keyvals...) }
func (l *TemporalSlogLogger) Error(msg string, keyvals ...interface{}) { l.log(slog.LevelError, msg, keyvals...) }

func (l *TemporalSlogLogger) log(level slog.Level, msg string, keyvals ...interface{}) {
	args := append(append([]any(nil), l.prefix...), normalizeKeyvals(keyvals)...)
	l.logger.Log(context.Background(), level, msg, args...)
}

func normalizeKeyvals(keyvals []interface{}) []any {
	if len(keyvals) == 0 {
		return nil
	}

	out := make([]any, 0, len(keyvals))
	for i := 0; i < len(keyvals); i += 2 {
		key := keyvals[i]
		var val any = nil
		if i+1 < len(keyvals) {
			val = keyvals[i+1]
		}

		switch k := key.(type) {
		case string:
			out = append(out, k, val)
		default:
			out = append(out, fmt.Sprint(k), val)
		}
	}
	return out
}

