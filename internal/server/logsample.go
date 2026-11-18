package server

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
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
	mu       sync.Mutex
	rate     float64
	burst    float64
	tokens   float64
	last     time.Time
	dropped  map[string]int // per message
	total    int
	full     func() bool // the log file reached its cap (nil: never)
	fullSaid bool
	now      func() time.Time
}

// Bounds of the per-message counters of dropped lines.
const (
	maxDropKeys     = 16
	maxDropKeyBytes = 40
)

func newSampledHandler(next slog.Handler, rate float64, burst int, now func() time.Time, full func() bool) *sampledHandler {
	return &sampledHandler{next: next, state: &sampleState{
		rate: rate, burst: float64(burst), tokens: float64(burst), now: now, full: full, dropped: map[string]int{},
	}}
}

func (h *sampledHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

// dropLocked counts a dropped record under its message.
func (s *sampleState) dropLocked(msg string) {
	key := auth.LogString(msg, maxDropKeyBytes)
	if _, ok := s.dropped[key]; !ok && len(s.dropped) >= maxDropKeys {
		key = "other"
	}
	s.dropped[key]++
	s.total++
}

func (h *sampledHandler) Handle(ctx context.Context, r slog.Record) error {
	s := h.state
	s.mu.Lock()
	now := s.now()
	if s.full != nil && s.full() {
		// The log file reached its cap: these lines are no longer written,
		// with one warning to say so.
		s.dropLocked(r.Message)
		say := !s.fullSaid
		s.fullSaid = true
		s.mu.Unlock()
		if say {
			warn := slog.NewRecord(now, slog.LevelWarn, "log file full: unsigned and rejected requests are no longer logged", 0)
			return h.next.Handle(ctx, warn)
		}
		return nil
	}
	if !s.last.IsZero() {
		if d := now.Sub(s.last).Seconds(); d > 0 {
			s.tokens = min(s.burst, s.tokens+d*s.rate)
		}
	}
	s.last = now
	if s.tokens < 1 {
		s.dropLocked(r.Message)
		s.mu.Unlock()
		return nil
	}
	s.tokens--
	var summary *slog.Record
	if s.total > 0 {
		rec := slog.NewRecord(now, slog.LevelWarn, "log lines dropped", 0)
		rec.AddAttrs(slog.Int("count", s.total))
		keys := make([]string, 0, len(s.dropped))
		for k := range s.dropped {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		by := make([]any, 0, len(keys))
		for _, k := range keys {
			by = append(by, slog.Int(k, s.dropped[k]))
		}
		rec.AddAttrs(slog.Group("by", by...))
		summary = &rec
		s.total = 0
		clear(s.dropped)
	}
	s.mu.Unlock()
	if summary != nil {
		_ = h.next.Handle(ctx, *summary)
	}
	return h.next.Handle(ctx, r)
}

func (h *sampledHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &sampledHandler{next: h.next.WithAttrs(attrs), state: h.state}
}

func (h *sampledHandler) WithGroup(name string) slog.Handler {
	return &sampledHandler{next: h.next.WithGroup(name), state: h.state}
}
