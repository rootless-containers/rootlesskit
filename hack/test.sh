#!/bin/bash
set -eu -o pipefail -x

cd $(dirname $0)/..

ref=rootless-containers/rootlesskit:test-unit
docker build -q --target test-unit -t $ref -f ./hack/test/Dockerfile .
docker run --privileged ${ref}

ref=rootless-containers/rootlesskit:test
# build -q but keep printing something (....) so as to avoid Travis timeout
(while sleep 60; do echo -n .; done) & docker build -q -t ${ref} -f ./hack/test/Dockerfile . ; kill %1

# TODO: add `--security-opt unconfined-paths=/sys` when https://github.com/docker/cli/pull/1347 gets merged.
# See https://github.com/rootless-containers/rootlesskit/pull/23 for the context.
#
# NOTE: bind-mounting /dev/net/tun is needed because we cannot mknod the tun device, at least with kernel < 4.18
#
# FIXME: --privilege is needed because the test suite compares rootlesskit with rootful `ip netns`.
# TODO: split the rootful test to separate container and remove --privilege
docker run \
       --rm \
       --security-opt seccomp=unconfined \
       --security-opt apparmor=unconfined \
       -v /dev/net/tun:/dev/net/tun \
       --privileged \
       ${ref}
