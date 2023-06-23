#!/bin/bash
source $(realpath $(dirname $0))/common.inc.sh
function benchmark::iperf3::port::udp() {
	statedir=$(mktemp -d)
	INFO "[benchmark:iperf3::port::udp] $@"
	IPERF3="iperf3"
	if echo "$@" | grep -q -- --detach-netns; then
		IPERF3="nsenter -n${statedir}/netns $IPERF3"
	fi
	$ROOTLESSKIT --state-dir=$statedir $@ $IPERF3 -s >/dev/null &
	rkpid=$!
	# wait for socket to be available
	sleep 3
	rootlessctl="rootlessctl --socket=$statedir/api.sock"
	portids=$($rootlessctl add-ports 127.0.0.1:5201:5201/tcp 127.0.0.1:5201:5201/udp)
	$rootlessctl list-ports
	sleep 3
	$IPERF3C 127.0.0.1 -u -b 100G
	$rootlessctl remove-ports $portids
	kill $rkpid
}

if [ $# -lt 1 ]; then
	ERROR "Usage: $0 PORTDRIVER [FLAGS...]"
	exit 1
fi
port=$1
shift 1
flags=$@

benchmark::iperf3::port::udp --net=slirp4netns --mtu=65520 --port-driver=${port} $flags
