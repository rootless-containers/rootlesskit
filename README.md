# RootlessKit: the gate to the rootless world

`rootlesskit` does `unshare` and `newuidmap/newgidmap` in a single command.

`rootlesskit` also supports network namespace isolation and userspace NAT using ["slirp"](#slirp).

Plan:
* Support netns with userspace NAT using `netstack` (No extra binary will be needed)
* Support netns with kernel NAT using SUID-enabled `lxc-user-nic``
  * We might also need some SUID binary for port forwarding
* Some cgroups stuff

## Setup

```console
$ go get github.com/AkihiroSuda/rootlesskit/cmd/rootlesskit
```

Requirements:
* Some distros such as Debian and Arch Linux require `echo 1 > /proc/sys/kernel/unprivileged_userns_clone`
* `newuidmap` and `newgidmap` need to be installed on the host. These commands are provided by the `uidmap` package.
* `/etc/subuid` and `/etc/subgid` should contain >= 65536 sub-IDs. e.g. `penguin:231072:65536`.

```console
$ id -u
1001
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
rootlesskit$ mount -t tmpfs none /anywhere
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
* Routing ICMP (ping) is not supported
* Specifying `--copy-up=/etc` is highly recommended unless `/etc/resolv.conf` is statically configured. Otherwise `/etc/resolv.conf` will be invalidated when it is recreated on the host.

Currently there are two slirp implementations supported by rootlesskit:
* `--net=vpnkit`, using [VPNKit](https://github.com/moby/vpnkit) (Recommended)
* `--net=vdeplug_slirp`, using [vdeplug_slirp](https://github.com/rd235/vdeplug_slirp)

Usage:

```console
$ rootlesskit --state=/run/user/1001/rootlesskit/foo --net=vpnkit --copy-up=/etc bash
rootlesskit$ ip a
1: lo: <LOOPBACK> mtu 65536 qdisc noop state DOWN group default qlen 1000
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
2: tap0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc fq_codel state UP group default qlen 1000
    link/ether 02:50:00:00:00:01 brd ff:ff:ff:ff:ff:ff
    inet 192.168.65.3/24 scope global tap0
       valid_lft forever preferred_lft forever
    inet6 fe80::50:ff:fe00:1/64 scope link tentative
       valid_lft forever preferred_lft forever
rootlesskit$ ip r
default via 192.168.65.1 dev tap0
192.168.65.0/24 dev tap0 proto kernel scope link src 192.168.65.3
rootlesskit$ cat /etc/resolv.conf 
nameserver 192.168.65.1
rootlesskit$ curl https://www.google.com
<!doctype html><html ...>...</html>
```

Default network configuration for `--net=vpnkit`:
* IP: 192.168.65.3/24
* Gateway: 192.168.65.1
* DNS: 192.168.65.1
* Host: 192.168.65.2

Default network configuration for `--net=vdeplug_slirp`:
* IP: 10.0.2.100/24
* Gateway: 10.0.2.2
* DNS: 10.0.2.3
* Host: 10.0.2.2, 10.0.2.3

Port forwarding:
```console
$ pid=$(cat /run/user/1001/rootlesskit/foo/child_pid)
$ socat -t -- TCP-LISTEN:8080,reuseaddr,fork EXEC:"nsenter -U -n -t $pid socat -t -- STDIN TCP4\:127.0.0.1\:80"
```

### Annex: how to install `--net=vpnkit`

See also https://github.com/moby/vpnkit

```console
$ git checkout https://github.com/moby/vpnkit.git
$ cd vpnkit
$ make
$ cp vpnkit.exe ~/bin
```

### Annex: how to install `--net=vdeplug_slirp`

You need to install the following components:

* https://github.com/rd235/s2argv-execs
* https://github.com/rd235/vdeplug4 (depends on `s2argv-execs`)
* https://github.com/rd235/libslirp
* https://github.com/rd235/vdeplug_slirp (depends on `vdeplug4` and `libslirp`)
