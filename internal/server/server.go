// Package server exposes the healthchecks ping endpoints and translates each
// ping into a metrics event.
package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"strconv"
	"time"
	"uuid"

	"github.com/vitalvas/gokit/fixedwindow"
	"github.com/vitalvas/gokit/xnet"
	"github.com/vitalvas/kasper/mux"
	"github.com/vitalvas/kasper/muxhandlers"

	"github.com/vitalvas/healthchecks-gateway/internal/config"
	"github.com/vitalvas/healthchecks-gateway/internal/metrics"
)

const (
	eventSuccess = "success"
	eventFail    = "fail"
	eventStart   = "start"
)

// ridLabel is the label added to the metric when a valid run id is provided.
const ridLabel = "rid"

const (
	bodyOK       = "OK"
	bodyNotFound = "OK (not found)"
)

// maxRequestBytes bounds the size of an incoming ping request body.
const maxRequestBytes = 1024

// rateLimitWindow is the fixed window for the request rate limits.
const rateLimitWindow = time.Minute

// rateLimitCleanup is how often expired rate-limit windows are swept.
const rateLimitCleanup = 5 * time.Minute

// rateLimitMaxKeys bounds the number of tracked rate-limit windows.
const rateLimitMaxKeys = 100000

// IPv4 and IPv6 rate-limit bucket prefix sizes.
const (
	ipMaskV4 = 32
	ipMaskV6 = 128
)

// Pusher records a healthcheck event in the metrics backend.
type Pusher interface {
	Push(ctx context.Context, event metrics.Event) error
}

// Clock returns the current time; it exists so tests can supply a fixed value.
type Clock func() time.Time

// Config holds the dependencies and settings required to build a Server.
type Config struct {
	Checks    map[string]config.Check
	Pusher    Pusher
	Log       *slog.Logger
	RateLimit config.RateLimitConfig
}

// Server routes ping requests and pushes the resulting events.
type Server struct {
	checks      map[string]config.Check
	pusher      Pusher
	log         *slog.Logger
	now         Clock
	pingRPM     int
	ipRPM       int
	pingLimiter *fixedwindow.Counter
	ipLimiter   *fixedwindow.Counter
}

// New creates a Server from the given configuration.
func New(cfg Config) *Server {
	return &Server{
		checks:      cfg.Checks,
		pusher:      cfg.Pusher,
		log:         cfg.Log,
		now:         time.Now,
		pingRPM:     cfg.RateLimit.CheckRPM,
		ipRPM:       cfg.RateLimit.IPRPM,
		pingLimiter: fixedwindow.New(rateLimitWindow, rateLimitMaxKeys, rateLimitCleanup),
		ipLimiter:   fixedwindow.New(rateLimitWindow, rateLimitMaxKeys, rateLimitCleanup),
	}
}

// Close releases the resources held by the Server's rate limiters.
func (s *Server) Close() {
	s.pingLimiter.Stop()
	s.ipLimiter.Stop()
}

// Handler builds the HTTP router for the gateway.
func (s *Server) Handler() (http.Handler, error) {
	// An empty config makes the middleware trust kasper's embedded default
	// proxy ranges (loopback and private/CGNAT/ULA networks) automatically.
	proxyHeaders, err := muxhandlers.ProxyHeadersMiddleware(muxhandlers.ProxyHeadersConfig{})
	if err != nil {
		return nil, fmt.Errorf("server: proxy headers middleware: %w", err)
	}

	sizeLimit, err := muxhandlers.RequestSizeLimitMiddleware(muxhandlers.RequestSizeLimitConfig{
		MaxBytes: maxRequestBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("server: request size limit middleware: %w", err)
	}

	r := mux.NewRouter()
	// Order: resolve the real client IP, enforce the per-IP limit before routing
	// (and thus before the check lookup), then cap the body size.
	r.Use(proxyHeaders)
	r.Use(s.ipRateLimit)
	r.Use(sizeLimit)

	r.HandleFunc("/ping/{id:uuid}", s.handleSuccess).Methods(http.MethodGet, http.MethodPost, http.MethodHead)
	r.HandleFunc("/ping/{id:uuid}/fail", s.handleFail).Methods(http.MethodGet, http.MethodPost, http.MethodHead)
	r.HandleFunc("/ping/{id:uuid}/start", s.handleStart).Methods(http.MethodGet, http.MethodPost, http.MethodHead)
	r.HandleFunc("/ping/{id:uuid}/{code:int}", s.handleExitCode).Methods(http.MethodGet, http.MethodPost, http.MethodHead)

	return r, nil
}

// ipRateLimit rejects requests from a client IP that exceeds the per-IP limit.
// The bucket key normalizes the source address to /32 (IPv4) or /128 (IPv6).
func (s *Server) ipRateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, err := ipBucket(r.RemoteAddr)
		if err != nil {
			s.log.Warn("cannot determine client ip", "error", err, "remote", r.RemoteAddr)
			writeText(w, http.StatusBadRequest, "invalid client address")
			return
		}

		if !s.ipLimiter.Allow(key, s.ipRPM) {
			s.log.Warn("ip rate limit exceeded", "bucket", key)
			writeText(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// ipBucket builds the per-IP rate-limit key from a request remote address.
func ipBucket(remoteAddr string) (string, error) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return "", fmt.Errorf("server: cannot parse ip from %q", remoteAddr)
	}

	masked, err := xnet.GetStripedAddress(ip, ipMaskV4, ipMaskV6)
	if err != nil {
		return "", fmt.Errorf("server: cannot mask ip %q: %w", host, err)
	}

	return fmt.Sprintf("ip:%s", masked.String()), nil
}

func (s *Server) handleSuccess(w http.ResponseWriter, r *http.Request) {
	s.record(w, r, eventSuccess, nil)
}

func (s *Server) handleFail(w http.ResponseWriter, r *http.Request) {
	s.record(w, r, eventFail, nil)
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	s.record(w, r, eventStart, nil)
}

func (s *Server) handleExitCode(w http.ResponseWriter, r *http.Request) {
	raw, _ := mux.VarGet(r, "code")

	code, err := strconv.Atoi(raw)
	if err != nil || code < 0 || code > 255 {
		writeText(w, http.StatusBadRequest, "invalid exit code")
		return
	}

	event := eventSuccess
	if code != 0 {
		event = eventFail
	}

	s.record(w, r, event, &code)
}

// pingQuery holds the optional query parameters accepted on a ping request.
type pingQuery struct {
	RID string `query:"rid"`
}

// record resolves the check, pushes the event, and writes the ping response.
// An unknown check is logged and answered with 200 to avoid client retries.
// An invalid rid query parameter is rejected with 400.
func (s *Server) record(w http.ResponseWriter, r *http.Request, event string, exitCode *int) {
	// Drain the body so the request size limit middleware can enforce its cap.
	// The body itself is diagnostic data that the gateway does not store.
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeText(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}

		writeText(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	id, _ := mux.VarGet(r, "id")

	var query pingQuery
	if err := mux.BindQuery(r, &query); err != nil {
		writeText(w, http.StatusBadRequest, "invalid query")
		return
	}

	if query.RID != "" {
		if _, err := uuid.Parse(query.RID); err != nil {
			writeText(w, http.StatusBadRequest, "invalid rid")
			return
		}
	}

	check, ok := s.checks[id]
	if !ok {
		s.log.Warn("ping for unknown check id", "check", id)
		writeText(w, http.StatusOK, bodyNotFound)
		return
	}

	pingKey := fmt.Sprintf("ping:%s", id)
	if !s.pingLimiter.Allow(pingKey, s.pingRPM) {
		s.log.Warn("ping rate limit exceeded", "bucket", pingKey)
		writeText(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

	err := s.pusher.Push(r.Context(), metrics.Event{
		Check:     id,
		Event:     event,
		Labels:    labelsWithRID(check.Labels, query.RID),
		ExitCode:  exitCode,
		Timestamp: s.now(),
	})
	if err != nil {
		s.log.Error("failed to push metric", "error", err, "check", id, "event", event)
	}

	writeText(w, http.StatusOK, bodyOK)
}

// labelsWithRID returns base unchanged when rid is empty, otherwise a copy with
// the rid label added so the configured label map is never mutated.
func labelsWithRID(base map[string]string, rid string) map[string]string {
	if rid == "" {
		return base
	}

	merged := make(map[string]string, len(base)+1)
	maps.Copy(merged, base)
	merged[ridLabel] = rid

	return merged
}

func writeText(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
