#!/bin/bash
source $(realpath $(dirname $0))/common.inc.sh
set -x

parent_ipv6="fdaa:aaaa:aaaa::1"
parent_dummy="dummy42"

sudo ip link add ${parent_dummy} type dummy
sudo ip link set dev ${parent_dummy} up
sudo ip addr add "${parent_ipv6}/64" dev ${parent_dummy}

tmp=$(mktemp -d)
echo "hello ipv6" >${tmp}/index.html

busybox httpd -f -p "[${parent_ipv6}]:8080" -h "${tmp}" &
pid=$!

$ROOTLESSKIT \
	--net=slirp4netns \
	--ipv6 \
	sh -euc "sleep 3; exec curl -fsSL http://[${parent_ipv6}]:8080"

kill -9 $pid || true
sudo ip link del ${parent_dummy}
rm -rf ${tmp}
