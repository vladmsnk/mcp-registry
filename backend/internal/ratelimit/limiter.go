// Package ratelimit implements a per-key token-bucket limiter used by the hub
// to cap the rate of outbound calls to any single registered MCP server (P3.13).
//
// Behaviour:
//   - One bucket per key (typically a server_id). Buckets are created lazily.
//   - Allow() is non-blocking: returns false when the bucket is empty.
//   - Buckets expire after IdleTTL of inactivity to bound memory.
//   - Safe for concurrent use.
package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Config controls the limiter. Zero values are treated as "no limit"
// (the package-level zero limiter then never rejects).
type Config struct {
	// RPS is the steady-state allowed rate per key (tokens per second).
	RPS float64
	// Burst is the bucket size — short-term overage allowed before rejection.
	Burst int
	// IdleTTL evicts buckets that have not been touched for this long.
	IdleTTL time.Duration
}

type entry struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

// Limiter is a per-key rate limiter. The zero value is "unlimited".
type Limiter struct {
	cfg      Config
	mu       sync.Mutex
	buckets  map[string]*entry
	enabled  bool
	stopOnce sync.Once
	stop     chan struct{}
}

// New returns a Limiter with the given config. When cfg.RPS <= 0, Allow always
// returns true (limit disabled). Caller is responsible for invoking Stop when
// shutting down to release the GC goroutine.
func New(cfg Config) *Limiter {
	if cfg.IdleTTL <= 0 {
		cfg.IdleTTL = 10 * time.Minute
	}
	l := &Limiter{
		cfg:     cfg,
		buckets: make(map[string]*entry),
		enabled: cfg.RPS > 0 && cfg.Burst > 0,
		stop:    make(chan struct{}),
	}
	if l.enabled {
		go l.gc()
	}
	return l
}

// Allow consumes one token from the bucket for `key`. Returns true on success,
// false when the bucket is empty (caller should reject the request).
func (l *Limiter) Allow(key string) bool {
	if l == nil || !l.enabled {
		return true
	}
	now := time.Now()
	l.mu.Lock()
	e, ok := l.buckets[key]
	if !ok {
		e = &entry{lim: rate.NewLimiter(rate.Limit(l.cfg.RPS), l.cfg.Burst)}
		l.buckets[key] = e
	}
	e.lastSeen = now
	l.mu.Unlock()
	return e.lim.AllowN(now, 1)
}

// Stop terminates the background GC goroutine. Idempotent.
func (l *Limiter) Stop() {
	if l == nil {
		return
	}
	l.stopOnce.Do(func() { close(l.stop) })
}

func (l *Limiter) gc() {
	t := time.NewTicker(l.cfg.IdleTTL / 2)
	defer t.Stop()
	for {
		select {
		case <-l.stop:
			return
		case now := <-t.C:
			l.mu.Lock()
			for k, e := range l.buckets {
				if now.Sub(e.lastSeen) > l.cfg.IdleTTL {
					delete(l.buckets, k)
				}
			}
			l.mu.Unlock()
		}
	}
}
