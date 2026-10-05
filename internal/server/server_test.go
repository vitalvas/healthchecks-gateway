package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitalvas/healthchecks-gateway/internal/config"
	"github.com/vitalvas/healthchecks-gateway/internal/metrics"
)

// discardLogger returns a logger that writes nothing, for use in tests.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

const knownID = "f81d4fae-7dec-11d0-a765-00a0c91e6bf6"

// pingURL builds a ping path for the given check id and optional suffix.
func pingURL(id, suffix string) string {
	if suffix == "" {
		return fmt.Sprintf("/ping/%s", id)
	}

	return fmt.Sprintf("/ping/%s/%s", id, suffix)
}

type fakePusher struct {
	events []metrics.Event
	err    error
}

func (f *fakePusher) Push(_ context.Context, event metrics.Event) error {
	f.events = append(f.events, event)
	return f.err
}

// highRPM is a rate limit large enough that general tests never trip it.
const highRPM = 1000000

func newTestServerWithLimits(t *testing.T, pusher Pusher, pingRPM, ipRPM int) *Server {
	t.Helper()

	srv := New(Config{
		Checks: map[string]config.Check{
			knownID: {Labels: map[string]string{"service": "api"}},
		},
		Pusher: pusher,
		Log:    discardLogger(),
		RateLimit: config.RateLimitConfig{
			CheckRPM: pingRPM,
			IPRPM:    ipRPM,
		},
	})
	srv.now = func() time.Time { return time.UnixMilli(1700000000000) }

	t.Cleanup(srv.Close)

	return srv
}

func newTestServerWithCheck(t *testing.T, pusher Pusher, check config.Check) http.Handler {
	t.Helper()

	srv := New(Config{
		Checks:    map[string]config.Check{knownID: check},
		Pusher:    pusher,
		Log:       discardLogger(),
		RateLimit: config.RateLimitConfig{CheckRPM: highRPM, IPRPM: highRPM},
	})
	srv.now = func() time.Time { return time.UnixMilli(1700000000000) }
	t.Cleanup(srv.Close)

	h, err := srv.Handler()
	require.NoError(t, err)

	return h
}

func newTestServer(t *testing.T, pusher Pusher) *Server {
	t.Helper()

	return newTestServerWithLimits(t, pusher, highRPM, highRPM)
}

func testHandler(t *testing.T, pusher Pusher) http.Handler {
	t.Helper()

	h, err := newTestServer(t, pusher).Handler()
	require.NoError(t, err)

	return h
}

func do(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func doBody(t *testing.T, h http.Handler, method, path string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, body)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func doFromIP(t *testing.T, h http.Handler, path, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func TestPingEvents(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		wantEvent    string
		wantExitCode *int
	}{
		{name: "success", path: pingURL(knownID, ""), wantEvent: eventSuccess},
		{name: "fail", path: pingURL(knownID, "fail"), wantEvent: eventFail},
		{name: "start", path: pingURL(knownID, "start"), wantEvent: eventStart},
		{name: "exit zero maps to success", path: pingURL(knownID, "0"), wantEvent: eventSuccess, wantExitCode: new(0)},
		{name: "exit nonzero maps to fail", path: pingURL(knownID, "1"), wantEvent: eventFail, wantExitCode: new(1)},
		{name: "exit max maps to fail", path: pingURL(knownID, "255"), wantEvent: eventFail, wantExitCode: new(255)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pusher := &fakePusher{}
			h := testHandler(t, pusher)

			rec := do(t, h, http.MethodGet, tt.path)

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, bodyOK, rec.Body.String())

			require.Len(t, pusher.events, 1)
			got := pusher.events[0]
			assert.Equal(t, knownID, got.Check)
			assert.Equal(t, tt.wantEvent, got.Event)
			assert.Equal(t, map[string]string{"service": "api"}, got.Labels)
			assert.Equal(t, time.UnixMilli(1700000000000), got.Timestamp)

			if tt.wantExitCode == nil {
				assert.Nil(t, got.ExitCode)
			} else {
				require.NotNil(t, got.ExitCode)
				assert.Equal(t, *tt.wantExitCode, *got.ExitCode)
			}
		})
	}
}

func TestHTTPMethods(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			pusher := &fakePusher{}
			h := testHandler(t, pusher)

			rec := do(t, h, method, pingURL(knownID, ""))

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Len(t, pusher.events, 1)
		})
	}

	t.Run("disallowed method", func(t *testing.T) {
		pusher := &fakePusher{}
		h := testHandler(t, pusher)

		rec := do(t, h, http.MethodDelete, pingURL(knownID, ""))

		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
		assert.Empty(t, pusher.events)
	})
}

func TestUnknownCheck(t *testing.T) {
	pusher := &fakePusher{}
	h := testHandler(t, pusher)

	rec := do(t, h, http.MethodGet, "/ping/11111111-1111-1111-1111-111111111111")

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, bodyNotFound, rec.Body.String())
	assert.Empty(t, pusher.events)
}

func TestPushFailureStillReturnsOK(t *testing.T) {
	pusher := &fakePusher{err: errors.New("backend down")}
	h := testHandler(t, pusher)

	rec := do(t, h, http.MethodGet, pingURL(knownID, ""))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, bodyOK, rec.Body.String())
	assert.Len(t, pusher.events, 1)
}

func TestInvalidExitCode(t *testing.T) {
	pusher := &fakePusher{}
	h := testHandler(t, pusher)

	rec := do(t, h, http.MethodGet, pingURL(knownID, "256"))

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, pusher.events)
}

func TestNamedPing(t *testing.T) {
	t.Run("allowed name adds label across events", func(t *testing.T) {
		tests := []struct {
			name         string
			suffix       string
			wantEvent    string
			wantExitCode *int
		}{
			{name: "success", suffix: "backup", wantEvent: eventSuccess},
			{name: "fail", suffix: "backup/fail", wantEvent: eventFail},
			{name: "start", suffix: "backup/start", wantEvent: eventStart},
			{name: "exit code", suffix: "backup/3", wantEvent: eventFail, wantExitCode: new(3)},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				pusher := &fakePusher{}
				h := newTestServerWithCheck(t, pusher, config.Check{
					Labels: map[string]string{"service": "api"},
					Names:  &[]string{"backup"},
				})

				rec := do(t, h, http.MethodGet, pingURL(knownID, tt.suffix))

				assert.Equal(t, http.StatusOK, rec.Code)
				require.Len(t, pusher.events, 1)
				got := pusher.events[0]
				assert.Equal(t, tt.wantEvent, got.Event)
				assert.Equal(t, map[string]string{"service": "api", "name": "backup"}, got.Labels)

				if tt.wantExitCode == nil {
					assert.Nil(t, got.ExitCode)
				} else {
					require.NotNil(t, got.ExitCode)
					assert.Equal(t, *tt.wantExitCode, *got.ExitCode)
				}
			})
		}
	})

	t.Run("empty names allows any name", func(t *testing.T) {
		pusher := &fakePusher{}
		h := newTestServerWithCheck(t, pusher, config.Check{Names: &[]string{}})

		rec := do(t, h, http.MethodGet, pingURL(knownID, "anything"))

		assert.Equal(t, http.StatusOK, rec.Code)
		require.Len(t, pusher.events, 1)
		assert.Equal(t, map[string]string{"name": "anything"}, pusher.events[0].Labels)
	})

	t.Run("nil names rejects named pings", func(t *testing.T) {
		pusher := &fakePusher{}
		h := newTestServerWithCheck(t, pusher, config.Check{Names: nil})

		rec := do(t, h, http.MethodGet, pingURL(knownID, "backup"))

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, bodyNotFound, rec.Body.String())
		assert.Empty(t, pusher.events)
	})

	t.Run("unlisted name rejected", func(t *testing.T) {
		pusher := &fakePusher{}
		h := newTestServerWithCheck(t, pusher, config.Check{Names: &[]string{"backup"}})

		rec := do(t, h, http.MethodGet, pingURL(knownID, "other"))

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, bodyNotFound, rec.Body.String())
		assert.Empty(t, pusher.events)
	})
}

func TestInvalidUUID(t *testing.T) {
	pusher := &fakePusher{}
	h := testHandler(t, pusher)

	rec := do(t, h, http.MethodGet, "/ping/not-a-uuid")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Empty(t, pusher.events)
}

func TestRunID(t *testing.T) {
	const rid = "11111111-2222-3333-4444-555555555555"

	t.Run("valid rid is added as a label", func(t *testing.T) {
		pusher := &fakePusher{}
		h := testHandler(t, pusher)

		rec := do(t, h, http.MethodGet, fmt.Sprintf("%s?rid=%s", pingURL(knownID, "start"), rid))

		assert.Equal(t, http.StatusOK, rec.Code)
		require.Len(t, pusher.events, 1)
		assert.Equal(t, map[string]string{"service": "api", "rid": rid}, pusher.events[0].Labels)
	})

	t.Run("no rid leaves labels unchanged", func(t *testing.T) {
		pusher := &fakePusher{}
		h := testHandler(t, pusher)

		rec := do(t, h, http.MethodGet, pingURL(knownID, ""))

		assert.Equal(t, http.StatusOK, rec.Code)
		require.Len(t, pusher.events, 1)
		assert.Equal(t, map[string]string{"service": "api"}, pusher.events[0].Labels)
	})

	t.Run("invalid rid is rejected with 400", func(t *testing.T) {
		pusher := &fakePusher{}
		h := testHandler(t, pusher)

		rec := do(t, h, http.MethodGet, fmt.Sprintf("%s?rid=not-a-uuid", pingURL(knownID, "")))

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Empty(t, pusher.events)
	})

	t.Run("invalid rid is rejected before unknown-check handling", func(t *testing.T) {
		pusher := &fakePusher{}
		h := testHandler(t, pusher)

		path := fmt.Sprintf("%s?rid=bad", pingURL("11111111-1111-1111-1111-111111111111", ""))
		rec := do(t, h, http.MethodGet, path)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Empty(t, pusher.events)
	})
}

func TestRequestSizeLimit(t *testing.T) {
	t.Run("body at the limit is accepted", func(t *testing.T) {
		pusher := &fakePusher{}
		h := testHandler(t, pusher)

		body := strings.NewReader(strings.Repeat("a", maxRequestBytes))
		rec := doBody(t, h, http.MethodPost, pingURL(knownID, ""), body)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, bodyOK, rec.Body.String())
		assert.Len(t, pusher.events, 1)
	})

	t.Run("body over the limit is rejected with 413", func(t *testing.T) {
		pusher := &fakePusher{}
		h := testHandler(t, pusher)

		body := strings.NewReader(strings.Repeat("a", maxRequestBytes+1))
		rec := doBody(t, h, http.MethodPost, pingURL(knownID, ""), body)

		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		assert.Empty(t, pusher.events)
	})

	t.Run("read error is rejected with 400", func(t *testing.T) {
		pusher := &fakePusher{}
		h := testHandler(t, pusher)

		rec := doBody(t, h, http.MethodPost, pingURL(knownID, ""), errReader{})

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Equal(t, "failed to read request body", rec.Body.String())
		assert.Empty(t, pusher.events)
	})
}

// errReader always fails, simulating a broken request body.
type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("read failure")
}

func TestIPRateLimit(t *testing.T) {
	t.Run("blocks after limit for same ip", func(t *testing.T) {
		pusher := &fakePusher{}
		h, err := newTestServerWithLimits(t, pusher, highRPM, 3).Handler()
		require.NoError(t, err)

		const addr = "203.0.113.5:4000"
		for i := range 3 {
			rec := doFromIP(t, h, pingURL(knownID, ""), addr)
			assert.Equal(t, http.StatusOK, rec.Code, "request %d", i)
		}

		rec := doFromIP(t, h, pingURL(knownID, ""), addr)
		assert.Equal(t, http.StatusTooManyRequests, rec.Code)
		assert.Len(t, pusher.events, 3)
	})

	t.Run("separate ips have separate buckets", func(t *testing.T) {
		pusher := &fakePusher{}
		h, err := newTestServerWithLimits(t, pusher, highRPM, 1).Handler()
		require.NoError(t, err)

		rec := doFromIP(t, h, pingURL(knownID, ""), "203.0.113.1:1000")
		assert.Equal(t, http.StatusOK, rec.Code)

		rec = doFromIP(t, h, pingURL(knownID, ""), "203.0.113.2:1000")
		assert.Equal(t, http.StatusOK, rec.Code)

		rec = doFromIP(t, h, pingURL(knownID, ""), "203.0.113.1:1000")
		assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	})

	t.Run("ip limit applies before unknown check handling", func(t *testing.T) {
		pusher := &fakePusher{}
		h, err := newTestServerWithLimits(t, pusher, highRPM, 1).Handler()
		require.NoError(t, err)

		const addr = "203.0.113.9:5000"
		const unknown = "11111111-1111-1111-1111-111111111111"

		rec := doFromIP(t, h, pingURL(unknown, ""), addr)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, bodyNotFound, rec.Body.String())

		rec = doFromIP(t, h, pingURL(unknown, ""), addr)
		assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	})

	t.Run("unparsable client address is rejected", func(t *testing.T) {
		pusher := &fakePusher{}
		h, err := newTestServerWithLimits(t, pusher, highRPM, highRPM).Handler()
		require.NoError(t, err)

		rec := doFromIP(t, h, pingURL(knownID, ""), "not-an-ip")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Empty(t, pusher.events)
	})
}

func TestPingRateLimit(t *testing.T) {
	pusher := &fakePusher{}
	h, err := newTestServerWithLimits(t, pusher, 2, highRPM).Handler()
	require.NoError(t, err)

	// Vary the source IP so the per-IP limit never trips before the per-ping one.
	for i := range 2 {
		rec := doFromIP(t, h, pingURL(knownID, ""), fmt.Sprintf("203.0.113.%d:1000", i+1))
		assert.Equal(t, http.StatusOK, rec.Code, "request %d", i)
	}

	rec := doFromIP(t, h, pingURL(knownID, ""), "203.0.113.100:1000")
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Len(t, pusher.events, 2)
}

func TestIPBucket(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		want    string
		wantErr bool
	}{
		{name: "ipv4 with port", addr: "203.0.113.5:4000", want: "ip:203.0.113.5"},
		{name: "ipv4 without port", addr: "203.0.113.5", want: "ip:203.0.113.5"},
		{name: "ipv6 with port", addr: "[2001:db8::1]:4000", want: "ip:2001:db8::1"},
		{name: "ipv6 without port", addr: "2001:db8::1", want: "ip:2001:db8::1"},
		{name: "invalid", addr: "not-an-ip", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ipBucket(tt.addr)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewUsesWallClock(t *testing.T) {
	srv := New(Config{Pusher: &fakePusher{}, Log: discardLogger()})
	t.Cleanup(srv.Close)
	require.NotNil(t, srv.now)

	delta := time.Since(srv.now())
	assert.Less(t, delta, time.Minute)
}
