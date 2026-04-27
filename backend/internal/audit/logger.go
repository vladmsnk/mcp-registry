package audit

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
)

// Sink persists audit events to durable storage.
type Sink interface {
	Insert(ctx context.Context, e Event) error
}

// Logger emits audit events to stdout (JSON for SIEM) and an optional Sink (DB).
type Logger struct {
	sink Sink
	log  *slog.Logger
}

// NewLogger returns a Logger that writes JSON lines to stdout and forwards to sink (may be nil).
func NewLogger(sink Sink) *Logger {
	return &Logger{
		sink: sink,
		log:  slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}
}

// Log emits the event. Sink failures are logged but never propagated — auditing must not break requests.
func (l *Logger) Log(ctx context.Context, e Event) {
	if l == nil {
		return
	}

	attrs := []slog.Attr{
		slog.String("event", "audit"),
		slog.String("action", e.Action),
		slog.String("status", e.Status),
		slog.String("actor_sub", e.ActorSub),
		slog.String("actor_username", e.ActorUsername),
		slog.Any("actor_roles", e.ActorRoles),
		slog.Int64("server_id", e.ServerID),
		slog.String("tool_name", e.ToolName),
		slog.Int64("latency_ms", e.LatencyMS),
		slog.String("request_id", e.RequestID),
		slog.String("ip", e.IP),
	}
	if e.Error != "" {
		attrs = append(attrs, slog.String("error", e.Error))
	}
	level := slog.LevelInfo
	if e.Status == StatusDenied || e.Status == StatusError {
		level = slog.LevelWarn
	}
	l.log.LogAttrs(ctx, level, "audit", attrs...)

	if l.sink != nil {
		if err := l.sink.Insert(ctx, e); err != nil {
			l.log.Error("audit sink insert failed",
				slog.String("error", err.Error()),
				slog.String("action", e.Action),
			)
		}
	}
}

// ClientIP returns the best-effort client IP from request headers.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(first)
	}
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		return xrip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
