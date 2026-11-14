package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/Karnchoudhary-99/ml-operator/internal/metrics"
)

// Handler returns the operator's HTTP handler: /metrics serves the
// collector's Prometheus text exposition, /healthz answers "ok" for
// liveness probes, and every other path is a 404. The handler reads
// the collector on every request, so scrapes always see current values.
func Handler(c *metrics.Collector) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		fmt.Fprint(w, c.Render())
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, "ok\n")
	})
	return mux
}

// Serve listens on addr and serves Handler(c) until ctx is cancelled,
// then shuts the server down gracefully (up to 5s for in-flight
// scrapes). It returns nil on a clean shutdown; a listen failure is
// returned immediately. Callers pass a context cancelled on SIGINT /
// SIGTERM — see cmd/operator.
func Serve(ctx context.Context, addr string, c *metrics.Collector) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	srv := &http.Server{Handler: Handler(c)}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
