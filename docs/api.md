# REST API

RootlessKit listens REST API on `${ROOTLESSKIT_STATE_DIR}/api.sock`.

```console
(host)$ rootlesskit --net=slirp4netns --port-driver=builtin bash
(rootlesskit)# curl -s --unix-socket "${ROOTLESSKIT_STATE_DIR}/api.sock" http://rootlesskit/v1/info | jq .
{
  "apiVersion": "1.1.0",
  "version": "0.14.0-beta.0",
  "stateDir": "/tmp/rootlesskit957151185",
  "childPID": 157684,
  "networkDriver": {
    "driver": "slirp4netns",
    "dns": [
      "10.0.2.3"
    ]
  },
  "portDriver": {
    "driver": "builtin",
    "protos": [
      "tcp",
      "udp"
    ]
  }
}
```

## openapi.yaml

See [`../pkg/api/openapi.yaml`](../pkg/api/openapi.yaml)

## rootlessctl CLI

`rootlessctl` is the CLI for the API.

```console
$ rootlessctl --help
NAME:
   rootlessctl - RootlessKit API client

USAGE:
   rootlessctl [global options] command [command options] [arguments...]

VERSION:
   0.14.0-beta.0

COMMANDS:
   list-ports    List ports
   add-ports     Add ports
   remove-ports  Remove ports
   info          Show info
   help, h       Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --debug         debug mode (default: false)
   --socket value  Path to api.sock (under the "rootlesskit --state-dir" directory), defaults to $ROOTLESSKIT_STATE_DIR/api.sock
   --help, -h      show help (default: false)
   --version, -v   print the version (default: false)
```

e.g., `rootlessctl --socket /foo/bar/sock info --json`
