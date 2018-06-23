# RootlessKit: the gate to the rootless world

`rootlesskit` does `unshare` and `newuidmap/newgidmap` in a single command.

`rootlesskit` also supports network namespace isolation and userspace NAT using ["slirp"](#slirp).

Plan:
* Support netns with userspace NAT using `netstack` (No extra binary will be needed)
* Support netns with kernel NAT using SUID-enabled `lxc-user-nic``
  * We might also need some SUID binary for port forwarding
* Some cgroups stuff

## Setup

```
$ go get github.com/AkihiroSuda/rootlesskit/cmd/rootlesskit
```

Requirements:
* Some distros such as Debian and Arch Linux require `echo 1 > /proc/sys/kernel/unprivileged_userns_clone`
* `newuidmap` and `newgidmap` need to be installed on the host. These commands are provided by the `uidmap` package.
* `/etc/subuid` and `/etc/subgid` should contain >= 65536 sub-IDs. e.g. `penguin:231072:65536`.

```
$ id -u
1001
$ grep ^$(whoami): /etc/subuid
penguin:231072:65536
$ grep ^$(whoami): /etc/subgid
penguin:231072:65536
```

## Usage

```
$ rootlesskit bash
rootlesskit$ cat /proc/self/uid_map
         0       1001          1
         1     231072      65536
rootlesskit$ cat /proc/self/gid_map
         0       1001          1
         1     231072      65536
rootlesskit$ cat /proc/self/setgroups
allow
rootlesskit$ mount -t tmpfs none /anywhere
rootlesskit$ touch /note_that_you_are_not_real_root
touch: cannot touch '/note_that_you_are_not_real_root': Permission denied
```

## Slirp

Remarks:
* Port forwarding is not supported yet
* ICMP (ping) is not supported

Currently there are two slirp implementations supported by rootlesskit:
* [vdeplug_slirp](#vdeplug_slirp)
* [VPNKit](#vpnkit)

### vdeplug_slirp
Dependencies:
* https://github.com/rd235/s2argv-execs
* https://github.com/rd235/vdeplug4 (depends on `s2argv-execs`)
* https://github.com/rd235/libslirp
* https://github.com/rd235/vdeplug_slirp (depends on `vdeplug4` and `libslirp`)

Usage:

```
$ rootlesskit --net=vdeplug_slirp bash
rootlesskit$ ip a
1: lo: <LOOPBACK> mtu 65536 qdisc noop state DOWN group default qlen 1000
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
2: tap0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc fq_codel state UP group default qlen 1000
    link/ether da:67:9b:30:19:b9 brd ff:ff:ff:ff:ff:ff
    inet 10.0.2.100/24 scope global tap0
       valid_lft forever preferred_lft forever
    inet6 fe80::d867:9bff:fe30:19b9/64 scope link 
       valid_lft forever preferred_lft forever
rootlesskit$ ip route
default via 10.0.2.2 dev tap0 
10.0.2.0/24 dev tap0 proto kernel scope link src 10.0.2.100 
rootlesskit$ cat /etc/resolv.conf 
nameserver 10.0.2.3
```

### VPNKit
Dependencies:
* https://github.com/moby/vpnkit

Usage:

```
$ rootlesskit --net=vpnkit bash
...
```
