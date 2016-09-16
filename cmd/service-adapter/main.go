package main

import (
	"log"
	"os"

	"github.com/pivotal-cf-experimental/redis-example-service-adapter/adapter"
	"github.com/pivotal-cf/on-demand-service-broker-sdk/serviceadapter"
)

func main() {
	stderrLogger := log.New(os.Stderr, "[redis-service-adapter] ", log.LstdFlags)
	manifestGenerator := adapter.ManifestGenerator{StderrLogger: stderrLogger}
	binder := adapter.Binder{StderrLogger: stderrLogger}
	serviceadapter.HandleCommandLineInvocation(os.Args, manifestGenerator, binder, nil)
}
