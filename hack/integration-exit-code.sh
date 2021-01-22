#!/bin/bash
source $(realpath $(dirname $0))/common.inc.sh

function test_exit_code() {
	args="$@"
	set +e
	for f in 0 42; do
		$ROOTLESSKIT $args sh -exc "exit $f" >/dev/null 2>&1
		code=$?
		if [ $code = $f ]; then
			INFO "exit $f works with \"$args\""
		else
			ERROR "exit $f does not work with \"$args\", got $code"
			exit 1
		fi
	done
}

test_exit_code --pidns=false
test_exit_code --pidns=true

# FIXME(#129): test_exit_code --pidns=true --reaper=true
