package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/CarambaG/taskflow/internal/auth"
	"github.com/CarambaG/taskflow/internal/domain"
	"golang.org/x/time/rate"
)

type Middleware struct {
	logger  *slog.Logger
	tokens  *auth.Manager
	limiter *ipLimiter
	seq     atomic.Uint64
}

func NewMiddleware(logger *slog.Logger, tokens *auth.Manager, rps float64, burst int) *Middleware {
	return &Middleware{logger: logger, tokens: tokens, limiter: newIPLimiter(rate.Limit(rps), burst)}
}

func (m *Middleware) RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if id == "" {
			id = fmt.Sprintf("%d-%d", time.Now().UnixNano(), m.seq.Add(1))
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *Middleware) Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				m.logger.ErrorContext(r.Context(), "panic recovered", "panic", recovered, "stack", string(debug.Stack()))
				writeError(m.logger, w, r, errorsInternal)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (m *Middleware) Log(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		wrapped := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(wrapped, r)
		m.logger.InfoContext(r.Context(), "http request",
			"method", r.Method, "path", r.URL.Path, "status", wrapped.status,
			"bytes", wrapped.bytes, "duration_ms", time.Since(started).Milliseconds(),
			"request_id", requestID(r.Context()), "remote_ip", clientIP(r))
	})
}

func (m *Middleware) RateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.limiter.allow(clientIP(r)) {
			w.Header().Set("Retry-After", "1")
			writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: errorBody{
				Code: "rate_limit_exceeded", Message: "too many requests", RequestID: requestID(r.Context()),
			}})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Middleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			writeError(m.logger, w, r, domain.ErrUnauthorized)
			return
		}
		id, err := m.tokens.Parse(strings.TrimSpace(parts[1]))
		if err != nil {
			writeError(m.logger, w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(withUserID(r.Context(), id)))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(payload []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(payload)
	w.bytes += n
	return n, err
}

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type ipLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rate     rate.Limit
	burst    int
}

func newIPLimiter(requestRate rate.Limit, burst int) *ipLimiter {
	return &ipLimiter{visitors: make(map[string]*visitor), rate: requestRate, burst: burst}
}

func (l *ipLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	item, ok := l.visitors[ip]
	if !ok {
		item = &visitor{limiter: rate.NewLimiter(l.rate, l.burst)}
		l.visitors[ip] = item
	}
	now := time.Now()
	item.lastSeen = now
	if len(l.visitors) > 10000 {
		for visitorIP, visitorItem := range l.visitors {
			if now.Sub(visitorItem.lastSeen) > 10*time.Minute {
				delete(l.visitors, visitorIP)
			}
		}
	}
	return item.limiter.Allow()
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

var errorsInternal = fmt.Errorf("internal middleware failure")
