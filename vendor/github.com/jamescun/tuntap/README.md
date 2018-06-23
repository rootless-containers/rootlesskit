TUN/TAP
=======

[![GoDoc](https://godoc.org/github.com/jamescun/tuntap?status.svg)](https://godoc.org/github.com/jamescun/tuntap) [![License](https://img.shields.io/badge/license-BSD-blue.svg)](LICENSE)

**NOTE:** This package is new and should be considered unstable, in terms of both API and function.

tuntap is a native wrapper for interfacing with TUN/TAP network devices in an idiomatic fashion.

Currently supported are Linux and Mac OS X.

    go get github.com/jamescun/tuntap


Configuration
-------------

The configuration required to open a TUN/TAP device varies by platform. The differences are noted below.

### Linux

When creating a TUN/TAP device, Linux expects to be given a name for the new interface, and a new interface will be allocated for it by the kernel module. If left blank, one will be generated `(tun|tap)([0-9]+)`.

    tap, err := tuntap.Tap("tap1") // created tap1 device if available
    tun, err := tuntap.Tun("")     // created tun device at first available id (tun0)


### Mac OS X

On startup, the Mac OS X TUN/TAP kernel extension will allocate multiple TUN/TAP devices, up to the maximum number of each. When creating a TUN/TAP device, Mac OS X expects to be given a path to an unused device. If left blank, this package will attempt to find the first unused one.

    tap, err := tuntap.Tap("/dev/tap1") // open tap1 device if unused
    tun, err := tuntap.Tun("")          // open first available tun device (tun0)

Additionally, unlike Linux, a TUN/TAP device is not "ready" on OS X until it has an address assigned to it. Any attempt to read from/write to the interface will fail with ErrBusy. It is safe to backoff and try again until a successful operation.


Examples
--------

See the [examples](examples) directory.
