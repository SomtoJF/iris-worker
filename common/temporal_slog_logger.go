package common

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"time"

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

func (l *TemporalSlogLogger) Debug(msg string, keyvals ...interface{}) {
	l.log(slog.LevelDebug, msg, keyvals...)
}
func (l *TemporalSlogLogger) Info(msg string, keyvals ...interface{}) {
	l.log(slog.LevelInfo, msg, keyvals...)
}
func (l *TemporalSlogLogger) Warn(msg string, keyvals ...interface{}) {
	l.log(slog.LevelWarn, msg, keyvals...)
}
func (l *TemporalSlogLogger) Error(msg string, keyvals ...interface{}) {
	l.log(slog.LevelError, msg, keyvals...)
}

func (l *TemporalSlogLogger) log(level slog.Level, msg string, keyvals ...interface{}) {
	args := append(append([]any(nil), l.prefix...), normalizeKeyvals(keyvals)...)

	// Build a record with an adjusted PC so slog's AddSource reports the workflow/activity
	// callsite (not this adapter). slog.Logger doesn't support caller skip directly.
	ctx := context.Background()
	if !l.logger.Enabled(ctx, level) {
		return
	}

	pc := callerPC(4 + l.callerSkip)
	r := slog.NewRecord(time.Now(), level, msg, pc)
	r.Add(args...)
	_ = l.logger.Handler().Handle(ctx, r)
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

func callerPC(skip int) uintptr {
	// runtime.Callers skip:
	// 0 -> runtime.Callers
	// 1 -> callerPC
	// 2 -> TemporalSlogLogger.log
	// 3 -> TemporalSlogLogger.{Debug,Info,Warn,Error}
	// 4 -> the workflow/activity callsite
	pcs := make([]uintptr, 1)
	if n := runtime.Callers(skip, pcs); n < 1 {
		return 0
	}
	return pcs[0]
}
