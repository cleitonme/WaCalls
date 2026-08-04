package main

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	rateLimitIdleEvict       = 3 * time.Minute
	rateLimitJanitorInterval = time.Minute
)

type ipLimiterEntry struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

type ipRateLimiter struct {
	mu    sync.Mutex
	perIP map[string]*ipLimiterEntry
	rps   rate.Limit
	burst int
}

func newIPRateLimiter(rps float64) *ipRateLimiter {
	return &ipRateLimiter{
		perIP: map[string]*ipLimiterEntry{},
		rps:   rate.Limit(rps),
		burst: max(1, int(2*rps)),
	}
}

func (l *ipRateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.perIP[key]
	if !ok {
		e = &ipLimiterEntry{lim: rate.NewLimiter(l.rps, l.burst)}
		l.perIP[key] = e
	}
	e.lastSeen = time.Now()
	return e.lim.Allow()
}

func (l *ipRateLimiter) purge(idle time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-idle)
	for ip, e := range l.perIP {
		if e.lastSeen.Before(cutoff) {
			delete(l.perIP, ip)
		}
	}
}

func (l *ipRateLimiter) janitor(stop <-chan struct{}) {
	t := time.NewTicker(rateLimitJanitorInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			l.purge(rateLimitIdleEvict)
		}
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			trimmed := strings.TrimSpace(parts[0])
			if trimmed != "" {
				return trimmed
			}
		}
	}
	return host
}

func withRateLimit(h http.Handler, l *ipRateLimiter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if l == nil {
			h.ServeHTTP(w, r)
			return
		}
		if !l.allow(clientIP(r)) {
			w.Header().Set("Retry-After", "1")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limit exceeded"}`))
			return
		}
		h.ServeHTTP(w, r)
	})
}
