#!/bin/bash
source $(realpath $(dirname $0))/common.inc.sh
function test_propagation() {
	propagation=$1
	INFO "Testing --propagation=$propagation"
	d=$(mktemp -d)
	state=$d/state
	$ROOTLESSKIT --state-dir=$state --propagation=$propagation -- sleep infinity &
	job=$!
	until test -f $state/child_pid; do sleep 0.1; done
	pid=$(cat $state/child_pid)
	mkdir -p $d/a
	touch $d/a/before_mount
	sudo mount -t tmpfs none $d/a
	touch $d/a/after_mount
	case $propagation in
	private | rprivate)
		test -f /proc/$pid/root/$d/a/before_mount
		test ! -f /proc/$pid/root/$d/a/after_mount
		;;
	slave | rslave | shared | rshared)
		test ! -f /proc/$pid/root/$d/a/before_mount
		test -f /proc/$pid/root/$d/a/after_mount
		;;
	*)
		ERROR "Unknown propagation $propagation"
		exit 1
		;;
	esac
	sudo umount $d/a
	kill $job
	wait
	rm -rf $d
	INFO "Testing --propagation=$propagation with copy-up"
	case $propagation in
	private | rprivate | slave | rslave)
		$ROOTLESSKIT --propagation=$propagation --copy-up=/run echo test
		;;
	shared | rshared)
		INFO "(skipping, because known not to work)"
		;;
	*)
		ERROR "Unknown propagation $propagation"
		exit 1
		;;
	esac
}

test_propagation private
test_propagation rprivate
if findmnt -n -l -o propagation / | grep shared >/dev/null; then
	test_propagation slave
	test_propagation rslave
	test_propagation shared
	test_propagation rshared
else
	INFO "the propagation of / is not shared; skipping non-private tests"
fi
