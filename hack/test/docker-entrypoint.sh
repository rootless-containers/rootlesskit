#!/bin/bash
function INFO(){
    echo -e "\e[104m\e[97m[INFO]\e[49m\e[39m $@"
}
set -eu -o pipefail -x

## benchmark:iperf3

nohup iperf3 -s &
INFO "[benchmark:iperf3] slirp4netns"
rootlesskit --net=slirp4netns iperf3 -c 10.0.2.2 -t 60

INFO "[benchmark:iperf3] vpnkit"
rootlesskit --net=vpnkit iperf3 -c 192.168.65.2 -t 60

INFO "[benchmark:iperf3] vdeplug_slirp"
rootlesskit --net=vdeplug_slirp iperf3 -c 10.0.2.2 -t 60

kill %1
