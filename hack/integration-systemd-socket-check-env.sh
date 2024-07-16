#!/bin/bash

set -eu -o pipefail

OK_FILE=$1
ERR_FILE=$2
EXPECTED_LISTEN_FDS=$3

fail() {
  echo "$@" > "$ERR_FILE"
  exit 1
}

if ! [[ "${LISTEN_FDS:-}" =~ [1-9] ]]; then
  fail "LISTEN_FDS (${LISTEN_FDS:-}) is not set or not positive a number."
fi

if [[ "${LISTEN_FDS:-}" != "${EXPECTED_LISTEN_FDS}" ]]; then
  fail "LISTEN_FDS (${LISTEN_FDS}) is not equal to expected ${EXPECTED_LISTEN_FDS}."
fi

if [[ "${LISTEN_PID}" != "$$" ]]; then
  fail "LISTEN_PID (${LISTEN_PID}) is not equal to \$\$ ($$)."
fi

for ((i=0,fdnum=3; i<LISTEN_FDS; fdnum++, i++)); do
  fdpath="/proc/$$/fd/${fdnum}"
  if [[ ! -e "$fdpath" ]]; then
    fail "FD #${fdnum} does not exists"
  fi
done

touch "${OK_FILE}"
