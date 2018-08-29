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
	manifestGenerator := adapter.ManifestGenerator{
		StderrLogger: stderrLogger,
		ConfigPath:   ConfigPath,
	}

	bindConfig, err := adapter.LoadConfig(ConfigPath, stderrLogger)
	if err != nil {
		os.Exit(serviceadapter.ErrorExitCode)
	}
	binder := adapter.Binder{StderrLogger: stderrLogger, Config: bindConfig}

	handler := serviceadapter.CommandLineHandler{
		ManifestGenerator: manifestGenerator,
		Binder:            binder,
	}
	serviceadapter.HandleCLI(os.Args, handler)
}
