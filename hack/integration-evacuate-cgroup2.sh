#!/bin/bash
source $(realpath $(dirname $0))/common.inc.sh

# Test requires systemd, so skipped on CI.
# Should work on both unified mode and "hybrid" mode.

# NOTE: extra sed is for eliminating tty escape sequence
group="$(systemd-run --user -t -q -- $ROOTLESSKIT --cgroupns --pidns --evacuate-cgroup2=evac grep -oP '0::\K.*' /proc/self/cgroup | sed 's/[^[:print:]]//g')"
if [ "$group" != "/evac" ]; then
  ERROR "expected group \"/evac\", got \"${group}\"."
  exit 1
fi
