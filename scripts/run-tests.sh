#!/bin/bash -eu

ginkgo -randomizeSuites=true -randomizeAllSpecs=true -keepGoing=true -r "$@"
