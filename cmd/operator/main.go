// Command operator is the ml-operator entrypoint. It parses flags,
// prints the version, and runs the reconcile loop (currently a
// dependency-free demo loop; the Kubernetes watch lands with the
// controller-runtime phase).
package main

import (
	"flag"
	"fmt"
	"os"
)

var version = "0.1.0"

func main() {
	var (
		showVersion bool
		namespace   string
		metricsAddr string
	)
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.StringVar(&namespace, "namespace", "default", "namespace to watch for ModelDeployments")
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "address for the metrics endpoint")
	flag.Parse()

	if showVersion {
		fmt.Printf("ml-operator %s\n", version)
		return
	}

	fmt.Printf("ml-operator %s starting (namespace=%s metrics=%s)\n", version, namespace, metricsAddr)
	fmt.Println("reconcile loop: not yet connected to a cluster (see roadmap)")
	os.Exit(0)
}
