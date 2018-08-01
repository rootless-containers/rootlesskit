#!/bin/bash
set -eu -o pipefail -x

cd $(dirname $0)/..
docker build -t rootless-containers/rootlesskit:test -f ./hack/test/Dockerfile .
docker run -it --rm --privileged rootless-containers/rootlesskit:test
