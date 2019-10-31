#!/bin/bash -eu

go run github.com/onsi/ginkgo/ginkgo -randomizeSuites=true -randomizeAllSpecs=true -keepGoing=true -r "$@"
