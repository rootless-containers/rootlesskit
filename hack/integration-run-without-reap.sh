#!/bin/bash
# Integration test for runWithoutReap (--reaper=false path).
# Regression test for https://github.com/rootless-containers/rootlesskit/issues/557
source $(realpath $(dirname $0))/common.inc.sh

INFO "Testing runWithoutReap: command execution"
out=$($ROOTLESSKIT --reaper=false echo hello 2>&1)
if ! echo "$out" | grep -q "hello"; then
	ERROR "expected 'hello' in output, got: $out"
	exit 1
fi

INFO "Testing runWithoutReap: exit code propagation"
set +e
$ROOTLESSKIT --reaper=false sh -c "exit 42" >/dev/null 2>&1
code=$?
set -e
if [ $code != 42 ]; then
	ERROR "expected exit code 42, got $code"
	exit 1
fi

INFO "Testing runWithoutReap: TTY preservation"
# Use script(1) to allocate a PTY; verify the child sees a TTY
# and does not print "cannot set terminal process group" (issue #557).
tmp=$(mktemp -d)
script -qec "$ROOTLESSKIT --reaper=false sh -c 'tty; echo DONE'" "$tmp/typescript" > "$tmp/out" 2>&1
if grep -qi "cannot set terminal process group" "$tmp/out"; then
	ERROR "child lost its controlling terminal (setsid regression)"
	cat "$tmp/out"
	rm -rf "$tmp"
	exit 1
fi
if ! grep -q "DONE" "$tmp/out"; then
	ERROR "child did not complete"
	cat "$tmp/out"
	rm -rf "$tmp"
	exit 1
fi
rm -rf "$tmp"

INFO "===== All runWithoutReap tests passed ====="
