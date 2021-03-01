## Mount Propagation

The mount namespace created by RootlessKit has `rprivate` propagation by default.

Starting with v0.9.0, the propagation can be set to `rslave` by specifying `--propagation=rslave`.

The propagation can be also set to `rshared`, but known not to work with `--copy-up`.

Note that `rslave` and `rshared` do not work as expected when the host root filesystem isn't mounted with "shared".
(Use `findmnt -n -l -o propagation /` to inspect the current mount flag.)
