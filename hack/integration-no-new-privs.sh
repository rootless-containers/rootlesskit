#!/bin/bash
# Regression test for https://github.com/rootless-containers/rootlesskit/issues/551
source $(realpath $(dirname $0))/common.inc.sh

# The regular newuidmap/newgidmap are setuid or have file caps, so they cannot work with no_new_privs.
# Use unprivileged stand-ins that map only the current UID/GID.
tmp=$(mktemp -d)
trap "rm -rf ${tmp}" EXIT
cat >${tmp}/newuidmap <<'EOS'
#!/bin/sh
echo "$2 $3 $4" >/proc/$1/uid_map
EOS
cat >${tmp}/newgidmap <<'EOS'
#!/bin/sh
echo deny >/proc/$1/setgroups
echo "$2 $3 $4" >/proc/$1/gid_map
EOS
chmod +x ${tmp}/newuidmap ${tmp}/newgidmap
export PATH=${tmp}:${PATH}

function test_no_new_privs() {
	args="$@"
	INFO "Testing no_new_privs for args=${args}"
	# Without the fix, rootlesskit hangs, so use timeout.
	out=$(setpriv --no-new-privs timeout 30 $ROOTLESSKIT $args sh -ec 'echo "uid=$(id -u)"; grep -E "^(NoNewPrivs|CapEff|CapAmb):" /proc/self/status')
	echo "${out}"
	for expected in "uid=0" "NoNewPrivs:.1" "CapEff:.[0-9a-f]*[1-9a-f][0-9a-f]*$" "CapAmb:.0*$"; do
		if ! echo "${out}" | grep -qE "${expected}"; then
			ERROR "expected to match ${expected}"
			exit 1
		fi
	done
}

test_no_new_privs --pidns=false
test_no_new_privs --pidns=true
