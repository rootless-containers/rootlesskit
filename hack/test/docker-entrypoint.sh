#!/bin/bash
set -eu -o pipefail

function INFO(){
    echo -e "\e[104m\e[97m[INFO]\e[49m\e[39m $@"
}

ROOTLESSKIT="rootlesskit"
IPERF3C="iperf3 -t 60 -c"

function benchmark::iperf3::slirp4netns(){
    INFO "[benchmark:iperf3] slirp4netns ($@)"
    set -x
    $ROOTLESSKIT --net=slirp4netns $@ $IPERF3C 10.0.2.2
    set +x
}

function benchmark::iperf3::vpnkit(){
    INFO "[benchmark:iperf3] vpnkit ($@)"
    set -x
    $ROOTLESSKIT --net=vpnkit $@ $IPERF3C 192.168.65.2
    set +x
}

function benchmark::iperf3::vdeplug_slirp(){
    INFO "[benchmark:iperf3] vdeplug_slirp ($@)"
    set -x
    $ROOTLESSKIT --net=vdeplug_slirp $@ $IPERF3C 10.0.2.2
    set +x
}

function benchmark::iperf3::main(){
    iperf3 -s > /dev/null &
    iperf3pid=$!
    for mtu in 1500 4000 16384 65520; do
        benchmark::iperf3::slirp4netns --mtu=$mtu
        if [[ $mtu -gt 16424 ]]; then
            INFO "Skipping benchmark::iperf3::vpnkit --mtu=$mtu (MTU greater than 16424 is known not to work for VPNKit)"
        else
            if [[ $mtu -gt 4000 ]]; then
                INFO "Note: MTU greather than 4K might not be effective for VPNKit: https://twitter.com/mugofsoup/status/1017665057738641408"
            fi
            benchmark::iperf3::vpnkit --mtu=$mtu
        fi
        if [[ $mtu -ne 1500 ]]; then
            INFO "Skipping benchmark::iperf3::vdeplug_slirp --mtu=$mtu (non-1500 MTU is not effective for vdeplug_slirp)"
        else
            benchmark::iperf3::vdeplug_slirp --mtu=$mtu
        fi
    done
    kill $iperf3pid
}

benchmark::iperf3::main
