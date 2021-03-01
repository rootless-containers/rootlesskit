# Port Drivers

To the ports in the network namespace to the host network namespace, `--port-driver` needs to be specified.

The default value is `none` (do not expose ports).

| `--port-driver`      |  Throughput | Source IP
|----------------------|-------------|----------
| `slirp4netns`        | 6.89 Gbps   | Propagated
| `socat` (Deprecated) | 7.80 Gbps   | Always 127.0.0.1
| `builtin`            | 30.0 Gbps   | Always 127.0.0.1

([Benchmark: iperf3 from the parent to the child (Mar 8, 2020)](https://github.com/rootless-containers/rootlesskit/runs/492498728))

The `builtin` driver is fastest, but be aware that the source IP is not propagated and always set to 127.0.0.1.

### Exposing ports
For example, to expose 80 in the child as 8080 in the parent:

```console
$ rootlesskit --state-dir=/run/user/1001/rootlesskit/foo --net=slirp4netns --disable-host-loopback --copy-up=/etc --port-driver=builtin bash
rootlesskit$ rootlessctl --socket=/run/user/1001/rootlesskit/foo/api.sock add-ports 0.0.0.0:8080:80/tcp
1
rootlesskit$ rootlessctl --socket=/run/user/1001/rootlesskit/foo/api.sock list-ports
ID    PROTO    PARENTIP   PARENTPORT    CHILDPORT    
1     tcp      0.0.0.0    8080          80
rootlesskit$ rootlessctl --socket=/run/user/1001/rootlesskit/foo/api.sock remove-ports 1
1
```

You can also expose ports using `socat` and `nsenter` instead of RootlessKit's port drivers.
```console
$ pid=$(cat /run/user/1001/rootlesskit/foo/child_pid)
$ socat -t -- TCP-LISTEN:8080,reuseaddr,fork EXEC:"nsenter -U -n -t $pid socat -t -- STDIN TCP4\:127.0.0.1\:80"
```

### Exposing privileged ports
To expose privileged ports (< 1024), add `net.ipv4.ip_unprivileged_port_start=0` to `/etc/sysctl.conf` (or `/etc/sysctl.d`) and run `sudo sysctl --system`.

If you are using `builtin` driver, you can expose the privileged ports without changing the sysctl value, but you need to set `CAP_NET_BIND_SERVICE` on `rootlesskit` binary.

```console
$ sudo setcap cap_net_bind_service=ep $(pwd rootlesskit)
```
