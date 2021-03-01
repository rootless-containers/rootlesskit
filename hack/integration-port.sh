#!/bin/bash
source $(realpath $(dirname $0))/common.inc.sh

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
	tmp=$(mktemp -d)
	state_dir=${tmp}/state
	html_dir=${tmp}/html
	mkdir -p ${html_dir}
	echo "test_port ($args)" >${html_dir}/index.html
	$ROOTLESSKIT \
		--state-dir=${state_dir} \
		--net=slirp4netns \
		--disable-host-loopback \
		--copy-up=/etc \
		--port-driver=${port_driver} \
		${rootlesskit_args} \
		busybox httpd -f -v -p 80 -h ${html_dir} \
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
	kill -SIGTERM $(cat ${state_dir}/child_pid)
	wait $pid >/dev/null 2>&1 || true
	rm -rf $tmp
}

INFO "===== Port driver: builtin ====="
INFO "=== protocol \"tcp\" listens on both v4 and v6 ==="
test_port builtin http://127.0.0.1:8080 "should success" -p 0.0.0.0:8080:80/tcp
test_port builtin http://[::1]:8080 "should success" -p 0.0.0.0:8080:80/tcp

INFO "=== protocol \"tcp4\" is strictly v4-only ==="
test_port builtin http://127.0.0.1:8080 "should success" -p 0.0.0.0:8080:80/tcp4
test_port builtin http://[::1]:8080 "should fail" -p 0.0.0.0:8080:80/tcp4

INFO "=== protocol \"tcp6\" is strictly v4-only ==="
test_port builtin http://127.0.0.1:8080 "should fail" -p [::]:8080:80/tcp6
test_port builtin http://[::1]:8080 "should success" -p [::]:8080:80/tcp6

INFO "=== \"tcp4\" and \"tcp6\" do not conflict ==="
test_port builtin http://127.0.0.1:8080 "should success" -p 0.0.0.0:8080:80/tcp4 -p [::]:8080:80/tcp6

INFO "===== Port driver: slirp4netns (IPv4 only)====="
INFO "=== protocol \"tcp\" listens on v4 ==="
test_port slirp4netns http://127.0.0.1:8080 "should success" -p 0.0.0.0:8080:80/tcp

INFO "=== protocol \"tcp4\" is strictly v4-only ==="
test_port slirp4netns http://[::1]:8080 "should fail" -p 0.0.0.0:8080:80/tcp4

INFO "===== PASSING ====="
