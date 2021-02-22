#!/bin/bash
source $(realpath $(dirname $0))/common.inc.sh

# 220: "state dir gets broken when the parent process gets SIGKILLED and then restarted && --state-dir is set explicitly && --port-driver is set"
INFO "Test for https://github.com/rootless-containers/rootlesskit/issues/220"

state_dir=$(mktemp -d)

$ROOTLESSKIT --state-dir=${state_dir} --port-driver=builtin --net=slirp4netns sleep infinity &
pid=$!
sleep 2
kill -9 $pid

# make sure API socket is functional after killing the parent and restarting.
$ROOTLESSKIT --state-dir=${state_dir} --port-driver=builtin --net=slirp4netns rootlessctl list-ports
