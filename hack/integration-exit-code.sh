#!/bin/bash
source $(realpath $(dirname $0))/common.inc.sh

function test_exit_code() {
	args="$@"
	INFO "Testig exit status for args=${args}"
	set +e
	for f in 0 42; do
		$ROOTLESSKIT $args sh -exc "exit $f" >/dev/null 2>&1
		code=$?
		if [ $code != $f ]; then
			ERROR "expected code $f, got $code"
			exit 1
		fi
	done
}

test_exit_code --pidns=false
test_exit_code --pidns=true --reaper=auto
test_exit_code --pidns=true --reaper=true
test_exit_code --pidns=true --reaper=false

function test_signal() {
	args="$@"
	INFO "Testig signal for args=${args}"
	set +e
	tmp=$(mktemp -d)
	$ROOTLESSKIT --state-dir=${tmp}/state $args sleep infinity >${tmp}/out 2>&1 &
	pid=$!
	sleep 1
	kill -SIGUSR1 $(cat ${tmp}/state/child_pid)
	wait $pid
	code=$?
	if [ $code != 255 ]; then
		ERROR "expected code 255, got $code"
		exit 1
	fi
	if ! grep -q "user defined signal 1" ${tmp}/out; then
		ERROR "didn't get SIGUSR1?"
		cat ${tmp}/out
		exit 1
	fi
	rm -rf $tmp
}

test_signal --pidns=false
test_signal --pidns=true --reaper=auto
test_signal --pidns=true --reaper=true
test_signal --pidns=true --reaper=false
