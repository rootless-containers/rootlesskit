# RootlessKit: the gate to the rootless world

`rootlesskit` does an equivalent of [`unshare(1)`](http://man7.org/linux/man-pages/man1/unshare.1.html) and [`newuidmap(1)`](http://man7.org/linux/man-pages/man1/newuidmap.1.html)/[`newgidmap(1)`](http://man7.org/linux/man-pages/man1/newgidmap.1.html) in a single command, for creating unprivileged [`user_namespaces`](http://man7.org/linux/man-pages/man7/user_namespaces.7.html) and [`mount_namespaces(7)`](http://man7.org/linux/man-pages/man7/user_namespaces.7.html) with [`subuid(5)`](http://man7.org/linux/man-pages/man5/subuid.5.html) and [`subgid(5)`](http://man7.org/linux/man-pages/man5/subgid.5.html).

`rootlesskit` also supports network namespace isolation and userspace NAT using ["slirp"](#slirp).

Plan:
* Support netns with userspace NAT using [`netstack`](https://github.com/google/netstack) (No extra binary will be needed)
* Support netns with kernel NAT using SUID-enabled [`lxc-user-nic(1)`](https://linuxcontainers.org/lxc/manpages/man1/lxc-user-nic.1.html)
  * We might also need some SUID binary for port forwarding
* Some cgroups stuff

## Setup

```console
$ go get github.com/rootless-containers/rootlesskit/cmd/rootlesskit
```

Requirements:
* Some distros such as Debian (excluding Ubuntu) and Arch Linux require `echo 1 > /proc/sys/kernel/unprivileged_userns_clone`
* `newuidmap` and `newgidmap` need to be installed on the host. These commands are provided by the `uidmap` package on most distros.
* `/etc/subuid` and `/etc/subgid` should contain >= 65536 sub-IDs. e.g. `penguin:231072:65536`.

```console
$ id -u
1001
$ whoami
penguin
$ grep ^$(whoami): /etc/subuid
penguin:231072:65536
$ grep ^$(whoami): /etc/subgid
penguin:231072:65536
```

## Usage

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
rootlesskit$ echo $HOME
/home/penguin
rootlesskit$ vi /home/penguin/override-foobar.conf
rootlesskit$ mount --bind /home/penguin/override-foobar.conf /etc/foobar.conf
rootlesskit$ touch /note_that_you_are_not_real_root
touch: cannot touch '/note_that_you_are_not_real_root': Permission denied
```

```console
$ rootlesskit --help
NAME:
   rootlesskit - the gate to the rootless world

USAGE:
   rootlesskit [global options] command [command options] [arguments...]

VERSION:
   0.0.0

COMMANDS:
     help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --debug                debug mode
   --state-dir value      state directory
   --net value            host, vdeplug_slirp, vpnkit (default: "host")
   --vpnkit-binary value  path of VPNKit binary for --net=vpnkit (default: "vpnkit")
   --mtu value            MTU for non-host network (default: 65520 for slirp4netns, 1500 for others) (default: 0)
   --copy-up value        mount a filesystem and copy-up the contents. e.g. "--copy-up=/etc" (typically required for non-host network)
   --copy-up-mode value   tmpfs+symlink (default: "tmpfs+symlink")
   --help, -h             show help
   --version, -v          print the version
```


## State directory

The following files will be created in the `--state-dir` directory:
* `lock`: lock file
* `child_pid`: decimal PID text that can be used for `nsenter(1)`.

Undocumented files are subject to change.

## Slirp

Remarks:
* Specifying `--copy-up=/etc` is highly recommended unless `/etc/resolv.conf` is statically configured. Otherwise `/etc/resolv.conf` will be invalidated when it is recreated on the host.

Currently there are three slirp implementations supported by rootlesskit:
* `--net=slirp4netns`, using [slirp4netns](https://github.com/rootless-containers/slirp4netns)
* `--net=vpnkit`, using [VPNKit](https://github.com/moby/vpnkit)
* `--net=vdeplug_slirp`, using [vdeplug_slirp](https://github.com/rd235/vdeplug_slirp)

Usage:

```console
$ rootlesskit --state=/run/user/1001/rootlesskit/foo --net=slirp4netns --copy-up=/etc bash
rootlesskit$ ip a
1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN group default qlen 1000
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
    inet 127.0.0.1/8 scope host lo
       valid_lft forever preferred_lft forever
    inet6 ::1/128 scope host
       valid_lft forever preferred_lft forever
2: tap0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc fq_codel state UP group default qlen 1000
    link/ether 42:b6:8d:e4:02:c4 brd ff:ff:ff:ff:ff:ff
    inet 10.0.2.100/24 scope global tap0
       valid_lft forever preferred_lft forever
    inet6 fe80::40b6:8dff:fee4:2c4/64 scope link
       valid_lft forever preferred_lft forever
rootlesskit$ ip r
default via 10.0.2.2 dev tap0
10.0.2.0/24 dev tap0 proto kernel scope link src 10.0.2.100
rootlesskit$ cat /etc/resolv.conf 
nameserver 10.0.2.3
rootlesskit$ curl https://www.google.com
<!doctype html><html ...>...</html>
```

Default network configuration for `--net=slirp4netns` and `--net=vdeplug_slirp`:
* IP: 10.0.2.100/24
* Gateway: 10.0.2.2
* DNS: 10.0.2.3
* Host: 10.0.2.2, 10.0.2.3

Default network configuration for `--net=vpnkit`:
* IP: 192.168.65.3/24
* Gateway: 192.168.65.1
* DNS: 192.168.65.1
* Host: 192.168.65.2


Port forwarding:
```console
$ pid=$(cat /run/user/1001/rootlesskit/foo/child_pid)
$ socat -t -- TCP-LISTEN:8080,reuseaddr,fork EXEC:"nsenter -U -n -t $pid socat -t -- STDIN TCP4\:127.0.0.1\:80"
```

### Routing ping packets

To route ping packets, you need to set up `net.ipv4.ping_group_range` properly.

```console
$ sudo sh -c "echo 0   2147483647  > /proc/sys/net/ipv4/ping_group_range"
```

Note: routing ping packets is not supported for `--net=vpnkit`.

### Annex: benchmark (MTU=1500)

Aug 1, 2018, on Travis: https://travis-ci.org/rootless-containers/rootlesskit/builds/410721610

* `--net=slirp4netns`: 1.07 Gbits/sec
* `--net=vpnkit`: 528 Mbits/sec
* `--net=vdeplug_slirp`: 771 Mbits/sec

Note: slirp4netns can reach 8.18 Gbits/sec with MTU=65520: https://github.com/rootless-containers/slirp4netns/pull/20

### Annex: how to install `slirp4netns` (required for `--net=slirp4netns`)

See also https://github.com/rootless-containers/slirp4netns

```console
$ git clone https://github.com/rootless-containers/slirp4netns
$ cd slirp4netns
$ ./autogen.sh && ./configure && make
$ cp slirp4netns ~/bin
```

RPM is also available for Fedora: https://rpms.remirepo.net/rpmphp/zoom.php?rpm=slirp4netns

```console
$ sudo dnf install slirp4netns
```

### Annex: how to install VPNKit (required for `--net=vpnkit`)

See also https://github.com/moby/vpnkit

```console
$ git clone https://github.com/moby/vpnkit.git
$ cd vpnkit
$ make
$ cp vpnkit.exe ~/bin/vpnkit
```

### Annex: how to install `vdeplug_slirp` (required for `--net=vdeplug_slirp`)

You need to install the following components:

* https://github.com/rd235/s2argv-execs
* https://github.com/rd235/vdeplug4 (depends on `s2argv-execs`)
* https://github.com/rd235/libslirp
* https://github.com/rd235/vdeplug_slirp (depends on `vdeplug4` and `libslirp`)

Please refer to README in the each of the components.
