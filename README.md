# RootlessKit: the gate to the rootless world

`rootlesskit` does `unshare` and `newuidmap/newgidmap` in a single command.

Plan:
* Support netns with userspace NAT using `netstack` or `slirp`
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
