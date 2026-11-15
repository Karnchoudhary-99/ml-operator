// Command operator is the ml-operator entrypoint. It parses flags,
// prints the version, and runs the reconcile loop (currently a
// dependency-free demo loop; the Kubernetes watch lands with the
// controller-runtime phase).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Karnchoudhary-99/ml-operator/internal/controller"
	"github.com/Karnchoudhary-99/ml-operator/internal/registry"
	"github.com/Karnchoudhary-99/ml-operator/internal/server"
)

var version = "0.1.0"

func main() {
	var (
		showVersion       bool
		configPath        string
		namespace         string
		metricsAddr       string
		reconcileInterval time.Duration
		applyMaxAttempts  int
		applyRetryBackoff time.Duration
		demo              bool
	)
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.StringVar(&configPath, "config", "", "path to an operator config file (YAML or JSON)")
	flag.StringVar(&namespace, "namespace", "default", "namespace to watch for ModelDeployments")
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "address for the metrics endpoint")
	flag.DurationVar(&reconcileInterval, "reconcile-interval", 30*time.Second, "how often to re-reconcile every deployment (0 disables the resync)")
	flag.IntVar(&applyMaxAttempts, "apply-max-attempts", 3, "how many times one cluster operation is tried before giving up")
	flag.DurationVar(&applyRetryBackoff, "apply-retry-backoff", 100*time.Millisecond, "base delay between cluster retries (doubles each time)")
	flag.BoolVar(&demo, "demo", false, "run the reconciler demo against the in-memory fake cluster and exit")
	flag.Parse()

	if showVersion {
		fmt.Printf("ml-operator %s\n", version)
		return
	}

	// Configuration resolves defaults < file < environment < flags;
	// a bad value anywhere fails startup instead of running on a
	// configuration nobody asked for.
	cfg, err := loadOperatorConfig(configPath, flagOverrides(
		setFlags(flag.CommandLine), namespace, metricsAddr,
		reconcileInterval, applyRetryBackoff, applyMaxAttempts))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ml-operator: %v\n", err)
		os.Exit(1)
	}

	if demo {
		if err := runDemo(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "demo failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	fmt.Printf("ml-operator %s starting (namespace=%s metrics=%s resync=%ds retries=%d@%dms)\n",
		version, cfg.Namespace, cfg.MetricsAddr, cfg.ReconcileIntervalSeconds,
		cfg.ApplyMaxAttempts, cfg.ApplyRetryBackoffMs)

	// The reconcile loop is not yet connected to a real cluster (see
	// roadmap), but the metrics endpoint is live from day one: the
	// collector already records reconciles, rollouts and scale
	// events, and the demo / future controller share it.
	reg := registry.New()
	cluster := controller.NewFakeCluster()
	rec := controller.New(reg, cluster)
	applyConfigToReconciler(cfg, rec)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if cfg.MetricsAddr != "" {
		go func() {
			if err := server.Serve(ctx, cfg.MetricsAddr, rec.Metrics); err != nil {
				fmt.Fprintf(os.Stderr, "metrics server: %v\n", err)
				stop()
			}
		}()
		fmt.Printf("metrics endpoint listening on %s (/metrics, /healthz)\n", cfg.MetricsAddr)
	}
	if cfg.ReconcileIntervalSeconds > 0 {
		go resyncLoop(ctx, reg, rec, time.Duration(cfg.ReconcileIntervalSeconds)*time.Second)
	}
	<-ctx.Done()
	fmt.Println("ml-operator shutting down")
}

// resyncLoop re-reconciles every registered deployment on a ticker,
// so drift is caught without waiting for an event. A deployment the
// resync cannot reconcile is logged, not fatal: the next tick retries
// it, and the operator keeps serving metrics.
func resyncLoop(ctx context.Context, reg *registry.Registry, rec *controller.Reconciler, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			resyncAll(reg, rec)
		}
	}
}

// resyncAll reconciles every registered deployment once and returns
// how many it reconciled. Errors are reported on stderr; the caller
// decides whether to keep going.
func resyncAll(reg *registry.Registry, rec *controller.Reconciler) int {
	n := 0
	for _, e := range reg.List() {
		key := e.Deployment.Key()
		if _, err := rec.Reconcile(key); err != nil {
			fmt.Fprintf(os.Stderr, "resync %s: %v\n", key, err)
			continue
		}
		n++
	}
	return n
}
