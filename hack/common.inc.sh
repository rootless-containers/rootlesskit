#!/bin/bash
set -eu -o pipefail

function INFO() {
	echo -e "\e[104m\e[97m[INFO]\e[49m\e[39m $@"
}

function ERROR() {
	echo >&2 -e "\e[101m\e[97m[ERROR]\e[49m\e[39m $@"
}

ROOTLESSKIT="rootlesskit"
IPERF3C="iperf3 -t 30 -c"
