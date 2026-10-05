package server_test

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitalvas/healthchecks-gateway/internal/config"
	"github.com/vitalvas/healthchecks-gateway/internal/metrics"
	"github.com/vitalvas/healthchecks-gateway/internal/server"
)

// TestGatewayToVictoriaMetrics drives a ping through the full router and real
// metrics client into a mock VictoriaMetrics endpoint, then asserts on the
// exposition line that actually arrives.
func TestGatewayToVictoriaMetrics(t *testing.T) {
	const checkID = "f81d4fae-7dec-11d0-a765-00a0c91e6bf6"

	var (
		mu    sync.Mutex
		lines []string
		paths []string
	)

	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		mu.Lock()
		paths = append(paths, r.URL.Path)
		lines = append(lines, string(body))
		mu.Unlock()

		w.WriteHeader(http.StatusNoContent)
	}))
	defer vm.Close()

	client := metrics.NewClient(vm.URL, 2*time.Second)
	srv := server.New(server.Config{
		Checks: map[string]config.Check{
			checkID: {Labels: map[string]string{"service": "api"}},
		},
		Pusher: client,
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		RateLimit: config.RateLimitConfig{
			CheckRPM: 1000,
			IPRPM:    1000,
		},
	})
	defer srv.Close()

	handler, err := srv.Handler()
	require.NoError(t, err)

	gateway := httptest.NewServer(handler)
	defer gateway.Close()

	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "success",
			path: fmt.Sprintf("/ping/%s", checkID),
			want: `healthcheck_event{check="f81d4fae-7dec-11d0-a765-00a0c91e6bf6",event="success",service="api"}`,
		},
		{
			name: "fail",
			path: fmt.Sprintf("/ping/%s/fail", checkID),
			want: `healthcheck_event{check="f81d4fae-7dec-11d0-a765-00a0c91e6bf6",event="fail",service="api"}`,
		},
		{
			name: "exit code",
			path: fmt.Sprintf("/ping/%s/2", checkID),
			want: `healthcheck_event{check="f81d4fae-7dec-11d0-a765-00a0c91e6bf6",event="fail",exit_code="2",service="api"}`,
		},
		{
			name: "start with run id",
			path: fmt.Sprintf("/ping/%s/start?rid=11111111-2222-3333-4444-555555555555", checkID),
			want: `healthcheck_event{check="f81d4fae-7dec-11d0-a765-00a0c91e6bf6",event="start",rid="11111111-2222-3333-4444-555555555555",service="api"}`,
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Get(fmt.Sprintf("%s%s", gateway.URL, tt.path))
			require.NoError(t, err)
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, "OK", string(body))

			mu.Lock()
			require.Len(t, lines, i+1)
			gotPath := paths[i]
			gotLine := lines[i]
			mu.Unlock()

			assert.Equal(t, "/api/v1/import/prometheus", gotPath)
			assert.True(t, strings.HasPrefix(gotLine, tt.want),
				"line %q does not start with %q", gotLine, tt.want)
		})
	}
}
