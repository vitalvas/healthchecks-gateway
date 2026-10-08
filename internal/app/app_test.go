package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
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
		path := filepath.Join(t.TempDir(), "bad.yaml")
		require.NoError(t, os.WriteFile(path, []byte("listen: [unterminated"), 0o600))

		err := Run([]string{"--config", path}, "test")
		require.Error(t, err)
	})

	t.Run("valid config serves until context is cancelled", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		content := `listen: "127.0.0.1:0"
victoriametrics:
  url: "http://127.0.0.1:8428"
checks:
  f81d4fae-7dec-11d0-a765-00a0c91e6bf6: {}
`
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		ctx, cancel := context.WithCancel(context.Background())

		done := make(chan error, 1)
		go func() {
			done <- run(ctx, path, discardLogger())
		}()

		time.Sleep(50 * time.Millisecond)
		cancel()

		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(3 * time.Second):
			t.Fatal("run did not shut down in time")
		}
	})

	t.Run("run propagates config load failure", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bad.yaml")
		require.NoError(t, os.WriteFile(path, []byte("checks:\n  not-a-uuid: {}\n"), 0o600))

		err := run(context.Background(), path, discardLogger())
		require.Error(t, err)
	})
}

func TestServeGracefulShutdown(t *testing.T) {
	httpServer := &http.Server{
		Addr:              "127.0.0.1:0",
		Handler:           http.NotFoundHandler(),
		ReadHeaderTimeout: time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, httpServer, discardLogger())
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

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

	err := serve(context.Background(), httpServer, discardLogger())
	require.Error(t, err)
}
