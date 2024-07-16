#!/bin/bash

srcdir=$(realpath $(dirname $0))
source "${srcdir}/common.inc.sh"

test_with_uuidd_daemon() {
    uuidd_tmpdir=$(mktemp -d)
    uuidd_sock="${uuidd_tmpdir}/uuidd.sock"
    systemd-socket-activate -l "${uuidd_sock}" "$ROOTLESSKIT" uuidd --no-pid --no-fork --socket-activation &
    pid=$!
    sleep 2
    uuidd -d -r -n 1 -s "${uuidd_sock}" || return 1
    uuidd -d -t -n 1 -s "${uuidd_sock}" || return 1
    uuidd -d -k -s "${uuidd_sock}" || return 1
    rm -r "${uuidd_tmpdir}" || return 1
    wait $pid || return 1
}

test_env_variables() {
   tmpdir=$(mktemp -d)
   sock1="${tmpdir}/sock1.sock"
   sock2="${tmpdir}/sock2.sock"
   sock3="${tmpdir}/sock3.sock"
   ## Test 1 socket
   timeout 30 systemd-socket-activate -l "${sock1}" "$ROOTLESSKIT" "${srcdir}/integration-systemd-socket-check-env.sh" "${tmpdir}/ok1" "${tmpdir}/fail1" 1 &
   pid=$!
   sleep 2
   curl --unix-socket "${sock1}" "http//example.com" >/dev/null 2>&1 || true # just trigger
   wait $pid
   if [[ ! -e "${tmpdir}/ok1" ]]; then return 1; fi
   ## Test 2 sockets
   timeout 30 systemd-socket-activate -l "${sock1}" -l "${sock2}" "$ROOTLESSKIT" "${srcdir}/integration-systemd-socket-check-env.sh" "${tmpdir}/ok2" "${tmpdir}/fail2" 2 &
   pid=$!
   sleep 2
   curl --unix-socket "${sock1}" "http//example.com" >/dev/null 2>&1 || true
   wait $pid
   if [[ ! -e "${tmpdir}/ok2" ]]; then return 1; fi
   ## Test 3 sockets
   timeout 30 systemd-socket-activate -l "${sock1}" -l "${sock2}" -l "${sock3}" "$ROOTLESSKIT" "${srcdir}/integration-systemd-socket-check-env.sh" "${tmpdir}/ok3" "${tmpdir}/fail3" 3 &
   pid=$!
   sleep 2
   curl --unix-socket "${sock1}" "http//example.com" >/dev/null 2>&1 || true
   wait $pid
   if [[ ! -e "${tmpdir}/ok3" ]]; then return 1; fi

   rm -r "${tmpdir}"
}

INFO "===== Systemd socket activation: uuidd daemon ====="
test_with_uuidd_daemon

INFO "===== Systemd socket activation: LISTEN_* variables check ====="
test_env_variables

INFO "===== PASSING ====="
