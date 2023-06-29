#!/bin/bash
source $(realpath $(dirname $0))/common.inc.sh
function benchmark::iperf3::port() {
	statedir=$(mktemp -d)
	INFO "[benchmark:iperf3::port] $@"
	portdriver="$1"
	shift
	flags="$@"
	IPERF3="iperf3"
	if echo "$@" | grep -q -- --detach-netns; then
		IPERF3="nsenter -n${statedir}/netns $IPERF3"
	fi
	$ROOTLESSKIT $flags --port-driver=$portdriver --state-dir=$statedir $IPERF3 -s >/dev/null &
	rkpid=$!
	# wait for socket to be available
	sleep 3
	rootlessctl="rootlessctl --socket=$statedir/api.sock"
	if [ $portdriver != "implicit" ]; then
		portid=$($rootlessctl add-ports 127.0.0.1:5201:5201/tcp)
		$rootlessctl list-ports
	fi
	$IPERF3C 127.0.0.1
	if [ $portdriver != "implicit" ]; then
		$rootlessctl remove-ports $portid
	fi
	kill $rkpid
}

if [ $# -lt 1 ]; then
	ERROR "Usage: $0 PORTDRIVER [FLAGS...]"
	exit 1
fi
portdriver=$1
shift 1
flags=$@

if ! echo $flags | grep -q -- "--net"; then
	flags="$flags --net=slirp4netns"
fi
flags="$flags --mtu=65520"

set -x
benchmark::iperf3::port ${portdriver} $flags
