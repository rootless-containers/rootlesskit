# Building RootlessKit

This document describes build-time options, including Go build tags for omitting certain network and port drivers.

## Build tags to omit drivers

To exclude specific drivers at compilation time, use Go build tags:

- Tag `no_vpnkit`: omits the VPNKit network driver implementation.
- Tag `no_gvisortapvsock`: omits the gvisor-tap-vsock network driver implementation and its port driver.
- Tag `no_slirp4netns`: omits the slirp4netns network driver implementation and its port driver.
- Tag `no_lxcusernic`: omits the lxc-user-nic network driver implementation.

Example:

- Build without VPNKit support:
  go build -tags no_vpnkit ./cmd/rootlesskit

Notes:
- If a disabled driver is selected at runtime (e.g., `--net=vpnkit` when built with `-tags no_vpnkit`), RootlessKit returns an error indicating that the driver was disabled at build time.