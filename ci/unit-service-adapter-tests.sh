#!/bin/bash -e

pushd $(dirname $0)/../../../../..
export GOPATH=$PWD

export PATH=$GOPATH/bin:$PATH

pushd src/github.com/pivotal-cf-experimental/redis-example-service-adapter

./scripts/run-tests.sh

popd
popd
