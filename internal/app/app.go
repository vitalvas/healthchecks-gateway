// Package app wires the configuration, metrics client, and HTTP server together
// and runs the gateway until the process is signaled to stop.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vitalvas/gokit/xflags"

	"github.com/vitalvas/healthchecks-gateway/internal/config"
	"github.com/vitalvas/healthchecks-gateway/internal/metrics"
	"github.com/vitalvas/healthchecks-gateway/internal/server"
)

// shutdownTimeout bounds the graceful shutdown of the HTTP server.
const shutdownTimeout = 10 * time.Second

// Run parses command-line arguments, loads the configuration, and serves the
// gateway until a termination signal is received. version is reported by the
// built-in --version flag.
func Run(args []string, version string) error {
	var opts struct {
		Config string `long:"config" short:"c" description:"Path to the YAML configuration file" required:"true"`
	}

	parser := xflags.New("healthchecks-gateway")
	parser.SetVersion(version)

	if err := parser.AddGroup("options", &opts); err != nil {
		return fmt.Errorf("register flags: %w", err)
	}

	if err := parser.Parse(args); err != nil {
		return err
	}

	if parser.Handled() {
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	return run(ctx, opts.Config, log)
}

// run loads the configuration, wires the server, and serves until ctx is
// cancelled. It is separate from Run so tests can drive the full path with a
// cancellable context instead of an OS signal.
func run(ctx context.Context, configPath string, log *slog.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	pusher := metrics.NewClient(cfg.VictoriaMetrics.URL, cfg.VictoriaMetrics.Timeout)
	srv := server.New(server.Config{
		Checks:    cfg.Checks,
		Pusher:    pusher,
		Log:       log,
		RateLimit: cfg.RateLimit,
	})
	defer srv.Close()

	handler, err := srv.Handler()
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	return serve(ctx, httpServer, log)
}

// serve starts the HTTP server and shuts it down gracefully when ctx is
// cancelled (in production, on SIGINT/SIGTERM).
func serve(ctx context.Context, httpServer *http.Server, log *slog.Logger) error {
	errCh := make(chan error, 1)

	go func() {
		log.Info("starting healthchecks gateway", "listen", httpServer.Addr)

		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}

		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err

	case <-ctx.Done():
		log.Info("shutting down")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		return httpServer.Shutdown(shutdownCtx)
	}
}
