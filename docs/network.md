## Network Drivers

RootlessKit provides several drivers for providing network connectivity:

* `--net=host`: use host network namespace (default)
* `--net=slirp4netns`: use [slirp4netns](https://github.com/rootless-containers/slirp4netns) (recommended)
* `--net=vpnkit`: use [VPNKit](https://github.com/moby/vpnkit)
* `--net=lxc-user-nic`: use `lxc-user-nic` (experimental)

[Benchmark: iperf3 from the child to the parent (Mar 8, 2020)](https://github.com/rootless-containers/rootlesskit/runs/492498728):

|                 Driver                |  MTU=1500  |  MTU=65520
|---------------------------------------|------------|-------------
|`slirp4netns`                          |  1.06 Gbps |  7.55 Gbps
|`slirp4netns` (with sandbox + seccomp) |  1.05 Gbps |  7.21 Gbps
|`vpnkit`                               |  0.60 Gbps |(Unsupported)
|`lxc-user-nic`                         |  31.4 Gbps |  30.9 Gbps
|(rootful veth)                         | (38.7 Gbps)| (40.8 Gbps)

### `--net=host` (default)

`--net=host` does not isolate the network namespace from the host.

Pros:
* No performance overhead
* Supports ICMP Echo (`ping`) when `/proc/sys/net/ipv4/ping_group_range` is configured

Cons:
* No permission for network-namespaced operations, e.g. creating iptables rules, running `tcpdump`

To route ICMP Echo packets (`ping`), you need to write the range of GIDs to [`net.ipv4.ping_group_range`](http://man7.org/linux/man-pages/man7/icmp.7.html). 

```console
$ sudo sh -c "echo 0   2147483647  > /proc/sys/net/ipv4/ping_group_range"
```

### `--net=slirp4netns` (recommended)

`--net=slirp4netns` isolates the network namespace from the host and launch [slirp4netns](https://github.com/rootless-containers/slirp4netns) for providing usermode networking.

Pros:
* Possible to perform network-namespaced operations, e.g. creating iptables rules, running `tcpdump`
* Supports ICMP Echo (`ping`) when `/proc/sys/net/ipv4/ping_group_range` is configured
* Supports hardening using mount namespace and seccomp (`--slirp4netns-sandbox=auto`, `--slirp4netns-seccomp=auto`, since RootlessKit v0.7.0, slirp4netns v0.4.0)
* Supports IPv6 routing (`--ipv6`)

Cons:
* Extra performance overhead (but still faster than `--net=vpnkit`)
* Supports only TCP, UDP, and ICMP Echo packets


To use `--net=slirp4netns`, you need to install slirp4netns v0.4.0 or later.

```console
$ sudo dnf install slirp4netns
```

or

```console
$ sudo apt-get install slirp4netns
```

If binary package is not available for your distribution, install from the source:

```console
$ git clone https://github.com/rootless-containers/slirp4netns
$ cd slirp4netns
$ ./autogen.sh && ./configure && make
$ cp slirp4netns ~/bin
```

The network is configured as follows by default:
* IP: 10.0.2.100/24
* Gateway: 10.0.2.2
* DNS: 10.0.2.3

The network configuration can be changed by specifying custom CIDR, e.g. `--cidr=10.0.3.0/24` (requires slirp4netns v0.3.0+).

Specifying `--copy-up=/etc` is highly recommended unless `/etc/resolv.conf` on the host is statically configured. Otherwise `/etc/resolv.conf` in the RootlessKit's mount namespace will be unmounted when `/etc/resolv.conf` on the host is recreated, typically by NetworkManager or systemd-resolved.

It is also highly recommended to specyfy`--disable-host-loopback`. Otherwise ports listening on 127.0.0.1 in the host are accessible as 10.0.2.2 in the RootlessKit's network namespace.

Example session:

```console
$ rootlesskit --net=slirp4netns --copy-up=/etc --disable-host-loopback bash
rootlesskit$ ip a
1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN group default qlen 1000
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
    inet 127.0.0.1/8 scope host lo
       valid_lft forever preferred_lft forever
    inet6 ::1/128 scope host
       valid_lft forever preferred_lft forever
2: tap0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 65520 qdisc fq_codel state UP group default qlen 1000
    link/ether 46:dc:8d:09:fd:f2 brd ff:ff:ff:ff:ff:ff
    inet 10.0.2.100/24 scope global tap0
       valid_lft forever preferred_lft forever
    inet6 fe80::44dc:8dff:fe09:fdf2/64 scope link
       valid_lft forever preferred_lft forever
ootlesskit$ ip r
default via 10.0.2.2 dev tap0
10.0.2.0/24 dev tap0 proto kernel scope link src 10.0.2.100
rootlesskit$ cat /etc/resolv.conf 
nameserver 10.0.2.3
rootlesskit$ curl https://www.google.com
<!doctype html><html ...>...</html>
```

Starting with RootlessKit v0.7.0 + slirp4netns v0.4.0, `--slirp4netns-sandbox=auto/true/false` (enables mount namespace) and `--slirp4netns-seccomp=auto/true/false` (enables seccomp rules) can be used to harden the slirp4netns process.

### `--net=vpnkit`

`--net=vpnkit` isolates the network namespace from the host and launch [VPNKit](https://github.com/moby/vpnkit) for providing usermode networking.

Pros:
* Possible to perform network-namespaced operations, e.g. creating iptables rules, running `tcpdump`

Cons:
* Extra performance overhead
* Supports only TCP and UDP packets. No support for ICMP Echo (`ping`) unlike `--net=slirp4netns`, even if `/proc/sys/net/ipv4/ping_group_range` is configured.
* No support for IPv6.

To use `--net=vpnkit`, you need to install VPNkit.

```console
$ git clone https://github.com/moby/vpnkit.git
$ cd vpnkit
$ make
$ cp vpnkit.exe ~/bin/vpnkit
```

The network is configured as follows by default:
* IP: 192.168.65.3/24
* Gateway: 192.168.65.1
* DNS: 192.168.65.1

As in `--net=slirp4netns`, specifying `--copy-up=/etc` and `--disable-host-loopback` is highly recommended.
If `--disable-host-loopback` is not specified, ports listening on 127.0.0.1 in the host are accessible as 192.168.65.2 in the RootlessKit's network namespace.

### `--net=lxc-user-nic` (experimental)

`--net=lxc-user-nic` isolates the network namespace from the host and launch [`lxc-user-nic(1)`](https://linuxcontainers.org/lxc/manpages/man1/lxc-user-nic.1.html) SUID binary for providing kernel-mode NAT.

Pros:
* The least performance overhead
* Possible to perform network-namespaced operations, e.g. creating iptables rules, running `tcpdump`
* Supports ICMP Echo (`ping`) without `/proc/sys/net/ipv4/ping_group_range` configuration

Cons:
* Less secure
* Needs `/etc/lxc/lxc-usernet` configuration
* No support for IPv6.
* No support for `--detach-netns`

To use `lxc-user-nic`, you need to install `liblxc-common` package:
```console
$ sudo apt-get install liblxc-common
```

You also need to set up [`/etc/lxc/lxc-usernet`](https://linuxcontainers.org/lxc/manpages/man5/lxc-usernet.5.html):
```
# USERNAME TYPE BRIDGE COUNT
penguin    veth lxcbr0 1
```

The `COUNT` value needs to be increased to run multiple RootlessKit instances with `--net=lxc-user-nic` simultaneously.

It may take a few seconds to configure the interface using DHCP.

If you start and stop RootlessKit too frequently, you might use up all available DHCP addresses.
You might need to reset `/var/lib/misc/dnsmasq.lxcbr0.leases` and restart the `lxc-net` service.

Currently, the MAC address is always set to a random address.

## IPv6

The `--ipv6` flag (since v0.14.0, EXPERIMENTAL) enables IPv6 routing for slirp4netns network driver.
This flag is unrelated to port forwarding.

## Detaching network namespace
The `--detach-netns` flag (since v2.0.0) detaches network namespaces into `$ROOTLESSKIT_STATE_DIR/netns`
and executes the child command in the host's network namespace.

The child command can enter `$ROOTLESSKIT_STATE_DIR/netns` by itself to create nested network namespaces.
