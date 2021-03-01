## PID Namespace

When `--pidns` (since v0.5.0) is specified, RootlessKit executes the child process in a new PID namespace.
The RootlessKit child process becomes the init (PID=1).
When RootlessKit terminates, all the processes in the namespace are killed with `SIGKILL`.

See also [`pid_namespaces(7)`](http://man7.org/linux/man-pages/man7/pid_namespaces.7.html).

## Cgroup Namespace
When `--cgroupns` (since v0.10.0) is specified, RootlessKit executes the child process in a new cgroup namespace.

### Cgroup2 evacuation
Cgroup2 evacuation is supported since v0.13.0.

e.g., `systemd-run -p Delegate=yes --user -t rootlesskit --cgroupns --pidns --evacuate-cgroup2=evac --net=slirp4netns bash`

When the current process belongs to `/foo` group (visible under `/sys/fs/cgroup/foo`) and evacuation group name is like `bar`,
- All processes in the `/foo` group are moved to `/foo/bar` group, by writing PIDs into `/sys/fs/cgroup/foo/bar/cgroup.procs`
- As many controllers as possible are enabled for `/foo/*` groups, by writing `/sys/fs/cgroup/foo/cgroup.subtree_control`
