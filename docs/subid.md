# subid sources

The subid sources can be specified via the `--subid-source=(auto|dynamic|static)` flag.

The `auto` source is the default since RootlessKit v1.1.0.
Prior to v1.1.0, only the `static` source was supported.

## Auto
The `auto` source (`--subid-source=auto`) tries the `dynamic` source and fall backs to the `static` source on an error.

## Dynamic
The `dynamic` source (`--subid-source=dynamic`) executes the `/usr/bin/getsubids` binary to get the subids.

The `getsubuids` binary is known to be available for the following distributions:
- Fedora, since 35 (`dnf install shadow-utils-subid`)
- Alpine, since 3.16 (`apkg install shadow-subids`)
- Ubuntu, since 22.10 (`apt-get install uidmap`)

The `getsubids` binary typically reads subids from `/etc/subuid` and `/etc/subgid` as in the static mode,
but it can be also configured to use SSSD by specifying `subid: sss` in `/etc/nsswitch.conf`.

See also https://manpages.debian.org/testing/uidmap/getsubids.1.en.html .

## Static
The `static` source (`--subid-source=static`) reads subids from `/etc/subuid` and `/etc/subgid`.

`/etc/subuid` and `/etc/subgid` should contain more than 65536 sub-IDs. e.g. `penguin:231072:65536`. These files are automatically configured on most distributions.

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

See also https://rootlesscontaine.rs/getting-started/common/subuid/
