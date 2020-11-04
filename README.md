# RootlessKit: Linux-native fakeroot using user namespaces

RootlessKit is a Linux-native implementation of "fake root" using  [`user_namespaces(7)`](http://man7.org/linux/man-pages/man7/user_namespaces.7.html).
The purpose of RootlessKit is to run [Docker and Kubernetes as an unprivileged user (known as "Rootless mode")](https://github.com/rootless-containers/usernetes), so as to protect the real root on the host from potential container-breakout attacks.

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->


- [What RootlessKit actually does](#what-rootlesskit-actually-does)
- [Similar projects](#similar-projects)
- [Projects using RootlessKit](#projects-using-rootlesskit)
- [Setup](#setup)
  - [Requirements](#requirements)
    - [Distribution-specific hints](#distribution-specific-hints)
- [Usage](#usage)
  - [Full CLI options](#full-cli-options)
- [State directory](#state-directory)
- [Environment variables](#environment-variables)
- [PID Namespace](#pid-namespace)
- [Mount Propagation](#mount-propagation)
- [Network Drivers](#network-drivers)
  - [`--net=host` (default)](#--nethost-default)
  - [`--net=slirp4netns` (recommended)](#--netslirp4netns-recommended)
  - [`--net=vpnkit`](#--netvpnkit)
  - [`--net=lxc-user-nic` (experimental)](#--netlxc-user-nic-experimental)
- [Port Drivers](#port-drivers)
  - [Exposing ports](#exposing-ports)
  - [Exposing privileged ports](#exposing-privileged-ports)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

## What RootlessKit actually does

RootlessKit creates [`user_namespaces(7)`](http://man7.org/linux/man-pages/man7/user_namespaces.7.html) and [`mount_namespaces(7)`](http://man7.org/linux/man-pages/man7/mount_namespaces.7.html), and executes [`newuidmap(1)`](http://man7.org/linux/man-pages/man1/newuidmap.1.html)/[`newgidmap(1)`](http://man7.org/linux/man-pages/man1/newgidmap.1.html) along with [`subuid(5)`](http://man7.org/linux/man-pages/man5/subuid.5.html) and [`subgid(5)`](http://man7.org/linux/man-pages/man5/subgid.5.html).

RootlessKit also supports isolating [`network_namespaces(7)`](http://man7.org/linux/man-pages/man7/network_namespaces.7.html) with userspace NAT using ["slirp"](#network-drivers).
Kernel-mode NAT using SUID-enabled [`lxc-user-nic(1)`](https://linuxcontainers.org/lxc/manpages/man1/lxc-user-nic.1.html) is also experimentally supported.

## Similar projects

Tools based on `LD_PRELOAD` (not enough to run rootless containers and yet lacks support for static binaries):
* [`fakeroot`](https://wiki.debian.org/FakeRoot)

Tools based on `ptrace(2)` (not enough to run rootless containers and yet slow):
* [`fakeroot-ng`](https://fakeroot-ng.lingnu.com/)
* [`proot`](https://proot-me.github.io/)

Tools based on `user_namespaces(7)` (as in RootlessKit, but without support for `--copy-up`, `--net`, ...):
* [`unshare -r`](http://man7.org/linux/man-pages/man1/unshare.1.html)
* [`podman unshare`](https://github.com/containers/libpod/blob/master/docs/source/markdown/podman-unshare.1.md)
* [`become-root`](https://github.com/giuseppe/become-root)

## Projects using RootlessKit

Container engines:
* [Docker/Moby](https://get.docker.com/rootless)
* [Podman](https://podman.io/) (since Podman v1.8.0)

Container image builders:
* [BuildKit](https://github.com/moby/buildkit): Next-generation `docker build` backend

Kubernetes distributions:
* [Usernetes](https://github.com/rootless-containers/usernetes): Docker & Kubernetes, installable under a non-root user's `$HOME`.
* [k3s](https://k3s.io/): Lightweight Kubernetes

## Setup

```console
$ go get github.com/rootless-containers/rootlesskit/cmd/rootlesskit
$ go get github.com/rootless-containers/rootlesskit/cmd/rootlessctl
```

or just run `make` to make binaries under `./bin` directory.

### Requirements

* `newuidmap` and `newgidmap` need to be installed on the host. These commands are provided by the `uidmap` package on most distributions.

* `/etc/subuid` and `/etc/subgid` should contain more than 65536 sub-IDs. e.g. `penguin:231072:65536`. These files are automatically configured on most distributions.

```console
$ id -u
1001
$ whoami
penguin
$ grep "^$(whoami):" /etc/subuid
penguin:231072:65536
$ grep "^$(whoami):" /etc/subgid
penguin:231072:65536
```

#### Distribution-specific hints

Debian (excluding Ubuntu):
* `sudo sh -c "echo 1 > /proc/sys/kernel/unprivileged_userns_clone"` is required

Arch Linux:
* `sudo sh -c "echo 1 > /proc/sys/kernel/unprivileged_userns_clone"` is required

RHEL/CentOS 7 (excluding RHEL/CentOS 8):
* `sudo sh -c "echo 28633 > /proc/sys/user/max_user_namespaces"` is required

To persist sysctl configurations, edit `/etc/sysctl.conf` or add a file under `/etc/sysctl.d`.

## Usage

Inside `rootlesskit`, your UID is mapped to 0 but it is not the real root:

```console
$ rootlesskit bash
rootlesskit$ id
uid=0(root) gid=0(root) groups=0(root),65534(nogroup)
rootlesskit$ ls -l /etc/shadow
-rw-r----- 1 nobody nogroup 1050 Aug 21 19:02 /etc/shadow
rootlesskit$ $ cat /etc/shadow
cat: /etc/shadow: Permission denied
```

Environment variables are kept untouched:

```console
$ rootlesskit bash
rootlesskit$ echo $USER
penguin
rootlesskit$ echo $HOME
/home/penguin
rootlesskit$ echo $XDG_RUNTIME_DIR
/run/user/1001
```

Filesystems can be isolated from the host with `--copy-up`:

```console
$ rootlesskit --copy-up=/etc bash
rootlesskit$ rm /etc/resolv.conf
rootlesskit$ vi /etc/resolv.conf
```

You can even create network namespaces with [Slirp](#network-drivers):

```console
$ rootlesskit --copy-up=/etc --copy-up=/run --net=slirp4netns --disable-host-loopback bash
rootlesskit$ ip netns add foo
...
```

Proc filesystem view:

```console
$ rootlesskit bash
rootlesskit$ cat /proc/self/uid_map
         0       1001          1
         1     231072      65536
rootlesskit$ cat /proc/self/gid_map
         0       1001          1
         1     231072      65536
rootlesskit$ cat /proc/self/setgroups
allow
```

### Full CLI options

```console
NAME:
   rootlesskit - Linux-native fakeroot using user namespaces

USAGE:
   rootlesskit [global options] [arguments...]

VERSION:
   0.11.0

DESCRIPTION:
   RootlessKit is a Linux-native implementation of "fake root" using user_namespaces(7).
   
   Web site: https://github.com/rootless-containers/rootlesskit
   
   Examples:
     # spawn a shell with a new user namespace and a mount namespace
     rootlesskit bash
   
     # make /etc writable
     rootlesskit --copy-up=/etc bash
   
     # set mount propagation to rslave
     rootlesskit --propagation=rslave bash
   
     # create a network namespace with slirp4netns, and expose 80/tcp on the namespace as 8080/tcp on the host
     rootlesskit --copy-up=/etc --net=slirp4netns --disable-host-loopback --port-driver=builtin -p 127.0.0.1:8080:80/tcp bash
   
   Note: RootlessKit requires /etc/subuid and /etc/subgid to be configured by the real root user.

GLOBAL OPTIONS:
   --debug                      debug mode (default: false)
   --state-dir value            state directory
   --net value                  network driver [host, slirp4netns, vpnkit, lxc-user-nic(experimental)] (default: "host")
   --slirp4netns-binary value   path of slirp4netns binary for --net=slirp4netns (default: "slirp4netns")
   --slirp4netns-sandbox value  enable slirp4netns sandbox (experimental) [auto, true, false] (the default is planned to be "auto" in future) (default: "false")
   --slirp4netns-seccomp value  enable slirp4netns seccomp (experimental) [auto, true, false] (the default is planned to be "auto" in future) (default: "false")
   --vpnkit-binary value        path of VPNKit binary for --net=vpnkit (default: "vpnkit")
   --lxc-user-nic-binary value  path of lxc-user-nic binary for --net=lxc-user-nic (default: "/usr/lib/x86_64-linux-gnu/lxc/lxc-user-nic")
   --lxc-user-nic-bridge value  lxc-user-nic bridge name (default: "lxcbr0")
   --mtu value                  MTU for non-host network (default: 65520 for slirp4netns, 1500 for others) (default: 0)
   --cidr value                 CIDR for slirp4netns network (default: 10.0.2.0/24)
   --ifname value               Network interface name (default: tap0 for slirp4netns and vpnkit, eth0 for lxc-user-nic)
   --disable-host-loopback      prohibit connecting to 127.0.0.1:* on the host namespace (default: false)
   --copy-up value              mount a filesystem and copy-up the contents. e.g. "--copy-up=/etc" (typically required for non-host network)
   --copy-up-mode value         copy-up mode [tmpfs+symlink] (default: "tmpfs+symlink")
   --port-driver value          port driver for non-host network. [none, builtin, slirp4netns, socat(deprecated)] (default: "none")
   --publish value, -p value    publish ports. e.g. "127.0.0.1:8080:80/tcp"
   --pidns                      create a PID namespace (default: false)
   --cgroupns                   create a cgroup namespace (default: false)
   --utsns                      create a UTS namespace (default: false)
   --ipcns                      create an IPC namespace (default: false)
   --propagation value          mount propagation [rprivate, rslave] (default: "rprivate")
   --help, -h                   show help (default: false)
   --version, -v                print the version (default: false)
```

## State directory

The following files will be created in the state directory, which can be specified with `--state-dir`:
* `lock`: lock file
* `child_pid`: decimal PID text that can be used for `nsenter(1)`.
* `api.sock`: REST API socket for `rootlessctl`. See [Port Drivers](#port-drivers) section.

If `--state-dir` is not specified, RootlessKit creates a temporary state directory on `/tmp` and removes it on exit.

Undocumented files are subject to change.

## Environment variables

The following environment variables will be set for the child process:
* `ROOTLESSKIT_STATE_DIR` (since v0.3.0): absolute path to the state dir
* `ROOTLESSKIT_PARENT_EUID` (since v0.8.0): effective UID
* `ROOTLESSKIT_PARENT_EGID` (since v0.8.0): effective GID

Undocumented environment variables are subject to change.

## PID Namespace

When `--pidns` (since v0.5.0) is specified, RootlessKit executes the child process in a new PID namespace.
The RootlessKit child process becomes the init (PID=1).
When RootlessKit terminates, all the processes in the namespace are killed with `SIGKILL`.

See also [`pid_namespaces(7)`](http://man7.org/linux/man-pages/man7/pid_namespaces.7.html).

## Mount Propagation

The mount namespace created by RootlessKit has `rprivate` propagation by default.

Starting with v0.9.0, the propagation can be set to `rslave` by specifying `--propagation=rslave`.

The propagation can be also set to `rshared`, but known not to work with `--copy-up`.

Note that `rslave` and `rshared` do not work as expected when the host root filesystem isn't mounted with "shared".
(Use `findmnt -n -l -o propagation /` to inspect the current mount flag.)

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


## Port Drivers

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
