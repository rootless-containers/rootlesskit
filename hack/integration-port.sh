#!/bin/bash
source $(realpath $(dirname $0))/common.inc.sh

ROOTLESSCTL="rootlessctl"

# test_port PORT_DRIVER CURL_URL EXPECTATION [ROOTLESSKIT ARGS...]
function test_port() {
	args="$@"
	port_driver="$1"
	curl_url="$2"
	expectation="$3"
	shift
	shift
	shift
	rootlesskit_args="$@"
	INFO "Testing port_driver=\"${port_driver}\" curl_url=\"${curl_url}\" expectation=\"${expectation}\" rootlesskit_args=\"${rootlesskit_args}\""
	net=slirp4netns
	if [ "${port_driver}" = "pesto" ]; then
		# The pesto port driver is specific to the pasta network driver
		net=pasta
	fi
	tmp=$(mktemp -d)
	state_dir=${tmp}/state
	html_dir=${tmp}/html
	mkdir -p ${html_dir}
	echo "test_port ($args)" >${html_dir}/index.html
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
		${rootlesskit_args} \
		${httpd} \
		2>&1 &
	pid=$!
	sleep 1

	set +e
	curl -fsSL ${curl_url}
	code=$?
	set -e
	if [ "${expectation}" = "should success" ]; then
		if [ ${code} != 0 ]; then
			ERROR "curl exited with ${code}"
			exit ${code}
		fi
	elif [ "${expectation}" = "should fail" ]; then
		if [ ${code} = 0 ]; then
			ERROR "curl should not success"
			exit 1
		fi
	else
		ERROR "internal error"
		exit 1
	fi

	INFO "Test pasing, stopping httpd (\"exit status 255\" is negligible here)"
	# child_pid does not exist when rootlesskit itself was expected to fail on startup
	if [ -f ${state_dir}/child_pid ]; then
		kill -SIGTERM $(cat ${state_dir}/child_pid) || true
	fi
	wait $pid >/dev/null 2>&1 || true
	wait_for_pasta_exit
	rm -rf $tmp
}

function wait_for_pasta_exit() {
	for i in $(seq 50); do
		pgrep -x pasta >/dev/null || break
		sleep 0.1
	done
}

# test_pesto_dynamic [ROOTLESSKIT ARGS...]
function test_pesto_dynamic() {
	rootlesskit_args="$@"
	INFO "Testing dynamic port management (rootlesskit_args=\"${rootlesskit_args}\")"
	tmp=$(mktemp -d)
	state_dir=${tmp}/state
	html_dir=${tmp}/html
	mkdir -p ${html_dir}
	echo "test_pesto_dynamic" >${html_dir}/index.html
	httpd="busybox httpd -f -v -p 80 -h ${html_dir}"
	if echo "${rootlesskit_args}" | grep -q -- --detach-netns; then
		# With --detach-netns, the child command runs in the host's network
		# namespace, so the server has to enter the detached netns explicitly.
		httpd="nsenter -n${state_dir}/netns ${httpd}"
	fi
	$ROOTLESSKIT \
		--state-dir=${state_dir} \
		--net=pasta \
		--disable-host-loopback \
		--copy-up=/etc \
		--port-driver=pesto \
		${rootlesskit_args} \
		${httpd} \
		2>&1 &
	pid=$!
	sleep 1
	api_sock=${state_dir}/api.sock

	INFO "= the port must not be reachable before add-ports ="
	if curl -fsSL http://127.0.0.1:8080; then
		ERROR "curl should not success before add-ports"
		exit 1
	fi

	INFO "= add-ports, then the port must be reachable ="
	id=$($ROOTLESSCTL --socket=${api_sock} add-ports 127.0.0.1:8080:80/tcp)
	curl -fsSL http://127.0.0.1:8080
	$ROOTLESSCTL --socket=${api_sock} list-ports --json | grep -q '"parentPort":8080'

	INFO "= adding a conflicting port must fail ="
	if $ROOTLESSCTL --socket=${api_sock} add-ports 127.0.0.1:8080:80/tcp; then
		ERROR "add-ports should fail for a conflicting port"
		exit 1
	fi

	INFO "= ChildIP matching the namespace address (10.0.2.100) is accepted ="
	# 10.0.2.100 is the first address of the default CIDR (10.0.2.0/24) + 100
	$ROOTLESSCTL --socket=${api_sock} add-ports 127.0.0.1:8081:10.0.2.100:80/tcp
	curl -fsSL http://127.0.0.1:8081

	INFO "= any other ChildIP must be rejected (pasta cannot honor it) ="
	if $ROOTLESSCTL --socket=${api_sock} add-ports 127.0.0.1:8083:10.9.9.9:80/tcp; then
		ERROR "add-ports should fail for an unsupported ChildIP"
		exit 1
	fi

	INFO "= unsupported proto (tcp6) must be rejected ="
	if $ROOTLESSCTL --socket=${api_sock} add-ports :8082:80/tcp6; then
		ERROR "add-ports should fail for tcp6"
		exit 1
	fi

	INFO "= udp rules can be added and removed ="
	udp_id=$($ROOTLESSCTL --socket=${api_sock} add-ports 127.0.0.1:5353:5353/udp)
	$ROOTLESSCTL --socket=${api_sock} list-ports --json | grep -q '"proto":"udp"'
	$ROOTLESSCTL --socket=${api_sock} remove-ports ${udp_id}

	INFO "= remove-ports, then new connections must be refused ="
	$ROOTLESSCTL --socket=${api_sock} remove-ports ${id}
	if curl -fsSL http://127.0.0.1:8080; then
		ERROR "curl should not success after remove-ports"
		exit 1
	fi

	INFO "= removing an unknown port ID must fail ="
	if $ROOTLESSCTL --socket=${api_sock} remove-ports ${id}; then
		ERROR "remove-ports should fail for an unknown ID"
		exit 1
	fi

	INFO "Test passing, stopping httpd (\"exit status 255\" is negligible here)"
	if [ -f ${state_dir}/child_pid ]; then
		kill -SIGTERM $(cat ${state_dir}/child_pid) || true
	fi
	wait $pid >/dev/null 2>&1 || true
	wait_for_pasta_exit
	rm -rf $tmp
}

INFO "===== Port driver: builtin ====="
INFO "=== protocol \"tcp\" listens on both v4 and v6 ==="
test_port builtin http://127.0.0.1:8080 "should success" -p 0.0.0.0:8080:80/tcp
test_port builtin http://[::1]:8080 "should success" -p 0.0.0.0:8080:80/tcp

INFO "=== protocol \"tcp4\" is strictly v4-only ==="
test_port builtin http://127.0.0.1:8080 "should success" -p 0.0.0.0:8080:80/tcp4
test_port builtin http://[::1]:8080 "should fail" -p 0.0.0.0:8080:80/tcp4

INFO "=== protocol \"tcp6\" is strictly v6-only ==="
test_port builtin http://127.0.0.1:8080 "should fail" -p [::]:8080:80/tcp6
test_port builtin http://[::1]:8080 "should success" -p [::]:8080:80/tcp6

INFO "=== v6-to-v6 ==="
test_port builtin http://[::1]:8080 "should success" -p [::]:8080:[::1]:80/tcp6
test_port builtin http://[::1]:8080 "should success" -p [::]:8080:[::1]:80/tcp

INFO "=== v6-to-v4 ==="
test_port builtin http://[::1]:8080 "should success" -p [::]:8080:[127.0.0.1]:80/tcp6
test_port builtin http://[::1]:8080 "should success" -p [::]:8080:[127.0.0.1]:80/tcp

INFO "=== v4-to-v6 ==="
test_port builtin http://127.0.0.1:8080 "should success" -p 0.0.0.0:8080:[::1]:80/tcp4
test_port builtin http://127.0.0.1:8080 "should success" -p 0.0.0.0:8080:[::1]:80/tcp

INFO "=== \"tcp4\" and \"tcp6\" do not conflict ==="
test_port builtin http://127.0.0.1:8080 "should success" -p 0.0.0.0:8080:80/tcp4 -p [::]:8080:80/tcp6

INFO "===== Port driver: slirp4netns (IPv4 only)====="
INFO "=== protocol \"tcp\" listens on v4 ==="
test_port slirp4netns http://127.0.0.1:8080 "should success" -p 0.0.0.0:8080:80/tcp

INFO "=== protocol \"tcp4\" is strictly v4-only ==="
test_port slirp4netns http://[::1]:8080 "should fail" -p 0.0.0.0:8080:80/tcp4

INFO "===== Port driver: pesto (--net=pasta) ====="
INFO "=== static publishing via -p ==="
test_port pesto http://127.0.0.1:8080 "should success" -p 0.0.0.0:8080:80/tcp
test_port pesto http://127.0.0.1:8080 "should success" -p 127.0.0.1:8080:80/tcp
test_port pesto http://127.0.0.1:8080 "should success" -p 0.0.0.0:8080:80/tcp4

INFO "=== port translation (8080 -> 80) ==="
test_port pesto http://127.0.0.1:8080 "should success" -p :8080:80/tcp

INFO "=== a port that is not published must not be reachable ==="
test_port pesto http://127.0.0.1:9090 "should fail" -p 0.0.0.0:8080:80/tcp

INFO "=== IPv6 requires --ipv6, as the pasta network driver is v4-only without it ==="
test_port pesto http://[::1]:8080 "should fail" -p [::]:8080:80/tcp6

INFO "=== protocol \"tcp6\" is strictly v6-only ==="
test_port pesto http://[::1]:8080 "should success" --ipv6 -p [::]:8080:80/tcp6
test_port pesto http://127.0.0.1:8080 "should fail" --ipv6 -p [::]:8080:80/tcp6
test_port pesto http://127.0.0.1:8080 "should fail" --ipv6 -p 0.0.0.0:8080:80/tcp6

INFO "=== protocol \"tcp4\" is strictly v4-only ==="
test_port pesto http://[::1]:8080 "should fail" --ipv6 -p [::]:8080:80/tcp4

INFO "=== the parent IP defaults to \"::\" for tcp6 ==="
test_port pesto http://[::1]:8080 "should success" --ipv6 -p :8080:80/tcp6

INFO "=== protocol \"tcp\" follows the address family of the parent IP ==="
test_port pesto http://[::1]:8080 "should success" --ipv6 -p [::]:8080:80/tcp
test_port pesto http://127.0.0.1:8080 "should fail" --ipv6 -p [::]:8080:80/tcp
test_port pesto http://127.0.0.1:8080 "should success" --ipv6 -p 0.0.0.0:8080:80/tcp
test_port pesto http://[::1]:8080 "should fail" --ipv6 -p 0.0.0.0:8080:80/tcp

INFO "=== specifying ChildIP is not supported ==="
test_port pesto http://[::1]:8080 "should fail" --ipv6 -p [::]:8080:[::1]:80/tcp6

INFO "=== dynamic port management via rootlessctl ==="
test_pesto_dynamic

INFO "=== with --detach-netns ==="
test_port pesto http://127.0.0.1:8080 "should success" -p 0.0.0.0:8080:80/tcp --detach-netns
test_pesto_dynamic --detach-netns

INFO "===== PASSING ====="
