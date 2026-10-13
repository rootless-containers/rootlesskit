#!/bin/bash
# Test source IP propagation of port drivers.
# - builtin (--source-ip-transparent): https://github.com/rootless-containers/rootlesskit/issues/648
# - gvisor-tap-vsock: https://github.com/rootless-containers/rootlesskit/issues/573
source $(realpath $(dirname $0))/common.inc.sh

ROOTLESSCTL="rootlessctl"

# The source IP is not propagated for loopback addresses
nonloopback="$(hostname -I | awk '{print $1}')"

# test_source_ip NET PORT_DRIVER [ROOTLESSKIT ARGS...]
function test_source_ip() {
	net="$1"
	port_driver="$2"
	shift 2
	rootlesskit_args="$@"
	INFO "Testing net=\"${net}\" port_driver=\"${port_driver}\" rootlesskit_args=\"${rootlesskit_args}\""
	tmp=$(mktemp -d)
	state_dir=${tmp}/state
	html_dir=${tmp}/html
	mkdir -p ${html_dir}/cgi-bin
	cat >${html_dir}/cgi-bin/ip <<'EOF'
#!/bin/sh
printf 'Content-Type: text/plain\r\n\r\n'
echo "${REMOTE_ADDR}"
EOF
	chmod +x ${html_dir}/cgi-bin/ip
	httpd="busybox httpd -f -v -p 80 -h ${html_dir}"
	if echo "${rootlesskit_args}" | grep -q -- --detach-netns; then
		# With --detach-netns, the child command runs in the host's network
		# namespace, so the server has to enter the detached netns explicitly.
		httpd="nsenter -n${state_dir}/netns ${httpd}"
	fi
	$ROOTLESSKIT \
		--state-dir=${state_dir} \
		--net=${net} \
		--disable-host-loopback \
		--copy-up=/etc \
		--port-driver=${port_driver} \
		--debug \
		-p 0.0.0.0:8080:80/tcp \
		${rootlesskit_args} \
		${httpd} \
		2>&1 &
	pid=$!
	sleep 2

	set -x
	$ROOTLESSCTL --socket=${state_dir}/api.sock info --json
	backend=$($ROOTLESSCTL --socket=${state_dir}/api.sock info --json | jq -r '.portDriver.extra.sourceIPTransparentBackend')
	# busybox httpd reports an IPv4 address in the IPv4-mapped IPv6 form, e.g., "[::ffff:172.17.0.2]"
	remote_addr=$(curl -fsSL http://${nonloopback}:8080/cgi-bin/ip | sed -e 's/^\[//' -e 's/\]$//' -e 's/^::ffff://')
	set +x

	kill -SIGTERM $(cat ${state_dir}/child_pid) || true
	wait $pid >/dev/null 2>&1 || true
	rm -rf $tmp

	if [ "${port_driver}" = "builtin" ]; then
		if [ "${backend}" = "none" ] || [ "${backend}" = "null" ] || [ -z "${backend}" ]; then
			ERROR "sourceIPTransparentBackend is \"${backend}\""
			exit 1
		fi
	fi
	if [ "${remote_addr}" != "${nonloopback}" ]; then
		ERROR "Expected REMOTE_ADDR to be \"${nonloopback}\", got \"${remote_addr}\""
		exit 1
	fi
	INFO "OK: sourceIPTransparentBackend=\"${backend}\", REMOTE_ADDR=\"${remote_addr}\""
}

test_source_ip slirp4netns builtin
test_source_ip slirp4netns builtin --detach-netns
test_source_ip gvisor-tap-vsock builtin
test_source_ip gvisor-tap-vsock builtin --detach-netns
test_source_ip gvisor-tap-vsock gvisor-tap-vsock
test_source_ip gvisor-tap-vsock gvisor-tap-vsock --detach-netns

INFO "===== PASSING ====="
