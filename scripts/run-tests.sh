#!/bin/bash -eu

if ! which ginkgo; then
  go install github.com/onsi/ginkgo/ginkgo
fi

ginkgo -randomizeSuites=true -randomizeAllSpecs=true -keepGoing=true -r "$@"
