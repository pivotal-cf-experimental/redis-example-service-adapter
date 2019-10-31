#!/bin/bash -eu

GOFLAGS="-mod=vendor" go run github.com/onsi/ginkgo/ginkgo -randomizeSuites=true -randomizeAllSpecs=true -keepGoing=true -r "$@"
