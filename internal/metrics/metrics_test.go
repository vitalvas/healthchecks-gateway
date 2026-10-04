package metrics

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatLine(t *testing.T) {
	ts := time.UnixMilli(1700000000000)

	tests := []struct {
		name  string
		event Event
		want  string
	}{
		{
			name: "no extra labels",
			event: Event{
				Check:     "f81d4fae-7dec-11d0-a765-00a0c91e6bf6",
				Event:     "success",
				Timestamp: ts,
			},
			want: `healthcheck_event{check="f81d4fae-7dec-11d0-a765-00a0c91e6bf6",event="success"} 1 1700000000000`,
		},
		{
			name: "labels sorted alphabetically",
			event: Event{
				Check:     "id",
				Event:     "fail",
				Labels:    map[string]string{"service": "api", "env": "prod"},
				Timestamp: ts,
			},
			want: `healthcheck_event{check="id",env="prod",event="fail",service="api"} 1 1700000000000`,
		},
		{
			name: "exit code added",
			event: Event{
				Check:     "id",
				Event:     "fail",
				ExitCode:  new(37),
				Timestamp: ts,
			},
			want: `healthcheck_event{check="id",event="fail",exit_code="37"} 1 1700000000000`,
		},
		{
			name: "exit code zero",
			event: Event{
				Check:     "id",
				Event:     "success",
				ExitCode:  new(0),
				Timestamp: ts,
			},
			want: `healthcheck_event{check="id",event="success",exit_code="0"} 1 1700000000000`,
		},
		{
			name: "label value escaping",
			event: Event{
				Check:     "id",
				Event:     "success",
				Labels:    map[string]string{"note": "a\\b\"c\nd"},
				Timestamp: ts,
			},
			want: `healthcheck_event{check="id",event="success",note="a\\b\"c\nd"} 1 1700000000000`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, formatLine(tt.event))
		})
	}
}

func TestClientPush(t *testing.T) {
	t.Run("success posts correct body and path", func(t *testing.T) {
		var gotPath, gotBody, gotContentType string

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotContentType = r.Header.Get("Content-Type")
			body, _ := io.ReadAll(r.Body)
			gotBody = string(body)

			assert.Equal(t, http.MethodPost, r.Method)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()

		client := NewClient(srv.URL, time.Second)
		err := client.Push(context.Background(), Event{
			Check:     "id",
			Event:     "success",
			Timestamp: time.UnixMilli(1700000000000),
		})
		require.NoError(t, err)

		assert.Equal(t, importPath, gotPath)
		assert.Equal(t, "text/plain", gotContentType)
		assert.Equal(t, `healthcheck_event{check="id",event="success"} 1 1700000000000`, gotBody)
	})

	t.Run("trims trailing slash from base url", func(t *testing.T) {
		var gotPath string

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		client := NewClient(fmt.Sprintf("%s/", srv.URL), time.Second)
		require.NoError(t, client.Push(context.Background(), Event{Check: "id", Event: "success"}))
		assert.Equal(t, importPath, gotPath)
	})

	t.Run("non-2xx status is an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
		}))
		defer srv.Close()

		client := NewClient(srv.URL, time.Second)
		err := client.Push(context.Background(), Event{Check: "id", Event: "success"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected status 500")
		assert.Contains(t, err.Error(), "boom")
	})

	t.Run("transport failure is an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := srv.URL
		srv.Close()

		client := NewClient(url, 100*time.Millisecond)
		err := client.Push(context.Background(), Event{Check: "id", Event: "success"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "push")
	})

	t.Run("invalid url fails request build", func(t *testing.T) {
		client := NewClient("http://\x00bad", time.Second)
		err := client.Push(context.Background(), Event{Check: "id", Event: "success"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "build request")
	})
}
