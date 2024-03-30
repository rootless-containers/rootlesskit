#!/bin/sh
set -e
if [ -z "$EXECED" ]
then
	systemd-socket-activate -E EXECED=1 -l /tmp/activate.sock socat ACCEPT-FD:3 EXEC:"rootlesskit $0",nofork 2>/dev/null &
	OUTPUT="$(curl --unix-socket /tmp/activate.sock http://localhost/hello 2>/dev/null)"
	[ "$(printf 'Hello\n' )" = "$OUTPUT" ] || exit 1
else
	[ "$LISTEN_FDS" = "1" ] || exit 1
	read -r REQUEST
	if [ "$(printf 'GET /hello HTTP/1.1\r\n')" = "$REQUEST" ]
	then
	    printf 'HTTP/1.1 200 OK\r\nContent-Length: 6\r\n\r\nHello\n'
	else
	    printf 'HTTP/1.1 400 Bad Request\r\nContent-Length: 5\r\n\r\nBad!\n'
	fi
fi
