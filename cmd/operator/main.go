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

	"github.com/Karnchoudhary-99/ml-operator/internal/controller"
	"github.com/Karnchoudhary-99/ml-operator/internal/registry"
	"github.com/Karnchoudhary-99/ml-operator/internal/server"
)

var version = "0.1.0"

func main() {
	var (
		showVersion bool
		namespace   string
		metricsAddr string
		demo        bool
	)
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.StringVar(&namespace, "namespace", "default", "namespace to watch for ModelDeployments")
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "address for the metrics endpoint")
	flag.BoolVar(&demo, "demo", false, "run the reconciler demo against the in-memory fake cluster and exit")
	flag.Parse()

	if showVersion {
		fmt.Printf("ml-operator %s\n", version)
		return
	}

	if demo {
		if err := runDemo(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "demo failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	fmt.Printf("ml-operator %s starting (namespace=%s metrics=%s)\n", version, namespace, metricsAddr)

	// The reconcile loop is not yet connected to a real cluster (see
	// roadmap), but the metrics endpoint is live from day one: the
	// collector already records reconciles, rollouts and scale
	// events, and the demo / future controller share it.
	reg := registry.New()
	cluster := controller.NewFakeCluster()
	rec := controller.New(reg, cluster)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if metricsAddr != "" {
		go func() {
			if err := server.Serve(ctx, metricsAddr, rec.Metrics); err != nil {
				fmt.Fprintf(os.Stderr, "metrics server: %v\n", err)
				stop()
			}
		}()
		fmt.Printf("metrics endpoint listening on %s (/metrics, /healthz)\n", metricsAddr)
	}
	<-ctx.Done()
	fmt.Println("ml-operator shutting down")
}
