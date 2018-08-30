package main

import (
	"log"
	"os"

	"github.com/pivotal-cf-experimental/redis-example-service-adapter/adapter"
	"github.com/pivotal-cf/on-demand-services-sdk/serviceadapter"
)

const ConfigPath = "/var/vcap/jobs/service-adapter/config/service-adapter.conf"

func main() {
	stderrLogger := log.New(os.Stderr, "[redis-service-adapter] ", log.LstdFlags)

	config, err := adapter.LoadConfig(ConfigPath, stderrLogger)
	if err != nil {
		os.Exit(serviceadapter.ErrorExitCode)
	}

	manifestGenerator := adapter.ManifestGenerator{
		StderrLogger: stderrLogger,
		Config:       config,
	}

	binder := adapter.Binder{
		StderrLogger: stderrLogger,
		Config:       config,
	}

	handler := serviceadapter.CommandLineHandler{
		ManifestGenerator: manifestGenerator,
		Binder:            binder,
	}

	serviceadapter.HandleCLI(os.Args, handler)
}
