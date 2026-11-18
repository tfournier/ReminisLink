package server

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Rate of the log lines of rejected requests (4xx/5xx answers and the
// rejections of internal/auth): beyond it, lines are dropped and counted,
// and one "log lines dropped" line reports them. Together with the bounded
// values (auth.LogString) it caps how fast an unauthenticated client can
// grow the log on the SD card.
const (
	RejectLogRate  = 5.0 // lines per second
	RejectLogBurst = 50
)

// sampledHandler is a slog.Handler that passes at most rate records per
// second (token bucket) to the next handler.
type sampledHandler struct {
	next  slog.Handler
	state *sampleState
}

type sampleState struct {
	mu      sync.Mutex
	rate    float64
	burst   float64
	tokens  float64
	last    time.Time
	dropped int
	now     func() time.Time
}

func newSampledHandler(next slog.Handler, rate float64, burst int, now func() time.Time) *sampledHandler {
	return &sampledHandler{next: next, state: &sampleState{rate: rate, burst: float64(burst), tokens: float64(burst), now: now}}
}

func (h *sampledHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h *sampledHandler) Handle(ctx context.Context, r slog.Record) error {
	s := h.state
	s.mu.Lock()
	now := s.now()
	if !s.last.IsZero() {
		if d := now.Sub(s.last).Seconds(); d > 0 {
			s.tokens = min(s.burst, s.tokens+d*s.rate)
		}
	}
	s.last = now
	if s.tokens < 1 {
		s.dropped++
		s.mu.Unlock()
		return nil
	}
	s.tokens--
	dropped := s.dropped
	s.dropped = 0
	s.mu.Unlock()
	if dropped > 0 {
		summary := slog.NewRecord(now, slog.LevelWarn, "log lines dropped", 0)
		summary.AddAttrs(slog.Int("count", dropped))
		_ = h.next.Handle(ctx, summary)
	}
	return h.next.Handle(ctx, r)
}

func (h *sampledHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &sampledHandler{next: h.next.WithAttrs(attrs), state: h.state}
}

func (h *sampledHandler) WithGroup(name string) slog.Handler {
	return &sampledHandler{next: h.next.WithGroup(name), state: h.state}
}
