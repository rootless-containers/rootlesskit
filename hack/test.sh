#!/bin/bash
set -eu -o pipefail -x

cd $(dirname $0)/..

# build -q but keep printing something (....) so as to avoid Travis timeout
(while sleep 60; do echo -n .; done) & docker build -q -t rootless-containers/rootlesskit:test -f ./hack/test/Dockerfile . ; kill %1
docker run -it --rm --privileged rootless-containers/rootlesskit:test
