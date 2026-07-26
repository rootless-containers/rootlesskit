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
	sudo /usr/lib/$(uname -m)-linux-gnu/lxc/lxc-net start || sudo /etc/init.d/lxc-net start || true
fi
$ROOTLESSKIT --net=${net} --copy-up=/etc --copy-up=/run --disable-host-loopback ${flags} -- nslookup example.com

# Test that a server process listening on the host loopback is not accessible from the isolated namespace
tmp=$(mktemp -d)
echo "hello host loopback" >${tmp}/index.html

busybox httpd -f -p 127.0.0.1:8080 -h ${tmp} &
pid=$!

sleep 1

statedir=$(mktemp -d)
CURL="timeout 3 curl -fsSL http://127.0.0.1:8080"
if echo "${flags}" | grep -q -- --detach-netns; then
	# With --detach-netns, the child command runs in the host's network namespace,
	# so it has to enter the detached network namespace explicitly.
	CURL="nsenter -n${statedir}/netns ${CURL}"
fi

if $ROOTLESSKIT --state-dir=${statedir} --net=${net} --copy-up=/etc --copy-up=/run --disable-host-loopback ${flags} -- ${CURL}; then
	ERROR "the host loopback should not be accessible from the isolated namespace"
	exit 1
fi

INFO "the host loopback is not accessible from the isolated namespace, as expected"

kill -9 $pid || true
rm -rf ${tmp} ${statedir}
