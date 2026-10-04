package app

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// discardLogger returns a logger that writes nothing, for use in tests.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRun(t *testing.T) {
	t.Run("missing required config flag", func(t *testing.T) {
		err := Run([]string{}, "test")
		require.Error(t, err)
	})

	t.Run("help is handled without error", func(t *testing.T) {
		err := Run([]string{"--help"}, "test")
		require.NoError(t, err)
	})

	t.Run("version is handled without error", func(t *testing.T) {
		err := Run([]string{"--version"}, "1.2.3")
		require.NoError(t, err)
	})

	t.Run("config load failure propagates", func(t *testing.T) {
		err := Run([]string{"--config", filepath.Join(t.TempDir(), "missing.yaml")}, "test")
		require.Error(t, err)
	})

	t.Run("valid config serves until signaled", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		content := `listen: "127.0.0.1:0"
victoriametrics:
  url: "http://127.0.0.1:8428"
checks:
  f81d4fae-7dec-11d0-a765-00a0c91e6bf6: {}
`
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		done := make(chan error, 1)
		go func() {
			done <- Run([]string{"--config", path}, "test")
		}()

		time.Sleep(50 * time.Millisecond)
		require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGTERM))

		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(3 * time.Second):
			t.Fatal("Run did not shut down in time")
		}
	})
}

func TestServeGracefulShutdown(t *testing.T) {
	httpServer := &http.Server{
		Addr:              "127.0.0.1:0",
		Handler:           http.NotFoundHandler(),
		ReadHeaderTimeout: time.Second,
	}

	done := make(chan error, 1)
	go func() {
		done <- serve(httpServer, discardLogger())
	}()

	time.Sleep(50 * time.Millisecond)
	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGTERM))

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("serve did not shut down in time")
	}
}

func TestServeListenError(t *testing.T) {
	httpServer := &http.Server{
		Addr:              "127.0.0.1:99999",
		Handler:           http.NotFoundHandler(),
		ReadHeaderTimeout: time.Second,
	}

	err := serve(httpServer, discardLogger())
	require.Error(t, err)
}
