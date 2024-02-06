#!/bin/bash
# Integration tests for network drivers.
# See also: benchmark-iperf3-net.sh

source $(realpath $(dirname $0))/common.inc.sh
if [ $# -lt 1 ]; then
	ERROR "Usage: $0 NETDRIVER [FLAGS...]"
	exit 1
fi
net=$1
shift 1
flags=$@
INFO "net=${net} flags=$@"

# Test DNS
set -x
if [ "${net}" = "lxc-user-nic" ]; then
	# ignore "lxc-net is already running" error
	sudo /usr/lib/$(uname -m)-linux-gnu/lxc/lxc-net start || true
fi
$ROOTLESSKIT --net=${net} --copy-up=/etc --copy-up=/run --disable-host-loopback ${flags} -- nslookup example.com
