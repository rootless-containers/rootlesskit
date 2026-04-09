# Port Drivers

To the ports in the network namespace to the host network namespace, `--port-driver` needs to be specified.

The default value is `none` (do not expose ports).

| `--port-driver`      |  Throughput | Source IP | Notes
|----------------------|-------------|----------|-------
| `slirp4netns`        | 8.03 Gbps   | Propagated |
| `builtin`            | 29.9 Gbps   | Propagated (since v3.0) | In the case of Rootless Docker, userland-proxy has to be disabled for propagating the source IP.
| `implicit`           | 37.6 Gbps   | Propagated | Requires `pasta` network
| `gvisor-tap-vsock` (Experimental) | 3.83 Gbps | Not propagated | Throughput is currently limited; see issue link below for improvement ideas.

Benchmark: iperf3 from the parent to the child is measured on GitHub Actions ([Apr 10, 2026](https://github.com/rootless-containers/rootlesskit/actions/runs/24200485791/job/70642399211))

The `builtin` driver is fast and should be the best choice for most use cases.

For [`pasta`](./network.md) networks, the `implicit` port driver is the best choice.

> [!NOTE]
> The `gvisor-tap-vsock` port driver is experimental.
> - Source IP is not propagated: https://github.com/rootless-containers/rootlesskit/issues/573
> - Current throughput is known to be slower than other drivers. We are tracking ideas for improving throughput here: https://github.com/rootless-containers/rootlesskit/issues/529

* To be documented: [`bypass4netns`](https://github.com/rootless-containers/bypass4netns) for native performance.

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


### Exposing privileged ports
To expose privileged ports (< 1024), add `net.ipv4.ip_unprivileged_port_start=0` to `/etc/sysctl.conf` (or `/etc/sysctl.d`) and run `sudo sysctl --system`.

If you are using `builtin` driver, you can expose the privileged ports without changing the sysctl value, but you need to set `CAP_NET_BIND_SERVICE` on `rootlesskit` binary.

```console
$ sudo setcap cap_net_bind_service=ep $(pwd rootlesskit)
```

### Note about IPv6

Specifying `0.0.0.0:8080:80/tcp` may cause listening on IPv6 as well as on IPv4.
Same applies to `[::]:8080:80/tcp`.

This behavior may sound weird but corresponds to [Go's behavior](https://github.com/golang/go/commit/071908f3d809245eda42bf6eab071c323c67b7d2),
so this is not a bug.

To specify IPv4 explicitly, use `tcp4` instead of `tcp`, e.g., `0.0.0.0:8080:80/tcp4`.
To specify IPv6 explicitly, use `tcp6`, e.g., `[::]:8080:80/tcp6`.

The `tcp4` and `tcp6` forms were introduced in RootlessKit v0.14.0.
The `tcp6` is currently supported only for `builtin` port driver.

## Build tags to omit port drivers

Build-time driver selection is documented in [BUILDING.md](BUILDING.md).
