package child

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/rootless-containers/rootlesskit/v2/pkg/common"
	"github.com/rootless-containers/rootlesskit/v2/pkg/copyup"
	"github.com/rootless-containers/rootlesskit/v2/pkg/messages"
	"github.com/rootless-containers/rootlesskit/v2/pkg/network"
	"github.com/rootless-containers/rootlesskit/v2/pkg/port"
	"github.com/rootless-containers/rootlesskit/v2/pkg/sigproxy"
	sigproxysignal "github.com/rootless-containers/rootlesskit/v2/pkg/sigproxy/signal"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

var propagationStates = map[string]uintptr{
	"private":  uintptr(unix.MS_PRIVATE),
	"rprivate": uintptr(unix.MS_REC | unix.MS_PRIVATE),
	"shared":   uintptr(unix.MS_SHARED),
	"rshared":  uintptr(unix.MS_REC | unix.MS_SHARED),
	"slave":    uintptr(unix.MS_SLAVE),
	"rslave":   uintptr(unix.MS_REC | unix.MS_SLAVE),
}

func createCmd(targetCmd []string) (*exec.Cmd, error) {
	var args []string
	if len(targetCmd) > 1 {
		args = targetCmd[1:]
	}
	cmd := exec.Command(targetCmd[0], args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}
	return cmd, nil
}

// mountSysfs is needed for mounting /sys/class/net
// when netns is unshared.
func mountSysfs(hostNetwork, evacuateCgroup2 bool) error {
	const cgroupDir = "/sys/fs/cgroup"
	if hostNetwork {
		if evacuateCgroup2 {
			// We need to mount tmpfs before cgroup2 to avoid EBUSY
			if err := unix.Mount("none", cgroupDir, "tmpfs", 0, ""); err != nil {
				return fmt.Errorf("failed to mount tmpfs on %s: %w", cgroupDir, err)
			}
			if err := unix.Mount("none", cgroupDir, "cgroup2", 0, ""); err != nil {
				return fmt.Errorf("failed to mount cgroup2 on %s: %w", cgroupDir, err)
			}
		}
		// NOP
		return nil
	}

	tmp, err := os.MkdirTemp("/tmp", "rksys")
	if err != nil {
		return fmt.Errorf("creating a directory under /tmp: %w", err)
	}
	defer os.RemoveAll(tmp)
	if !evacuateCgroup2 {
		if err := unix.Mount(cgroupDir, tmp, "", uintptr(unix.MS_BIND|unix.MS_REC), ""); err != nil {
			return fmt.Errorf("failed to create bind mount on %s: %w", cgroupDir, err)
		}
	}

	if err := unix.Mount("none", "/sys", "sysfs", 0, ""); err != nil {
		// when the sysfs in the parent namespace is RO,
		// we can't mount RW sysfs even in the child namespace.
		// https://github.com/rootless-containers/rootlesskit/pull/23#issuecomment-429292632
		// https://github.com/torvalds/linux/blob/9f203e2f2f065cd74553e6474f0ae3675f39fb0f/fs/namespace.c#L3326-L3328
		logrus.Warnf("failed to mount sysfs, falling back to read-only mount: %v", err)
		if err := unix.Mount("none", "/sys", "sysfs", uintptr(unix.MS_RDONLY), ""); err != nil {
			// when /sys/firmware is masked, even RO sysfs can't be mounted
			logrus.Warnf("failed to mount sysfs: %v", err)
		}
	}
	if evacuateCgroup2 {
		if err := unix.Mount("none", cgroupDir, "cgroup2", 0, ""); err != nil {
			return fmt.Errorf("failed to mount cgroup2 on %s: %w", cgroupDir, err)
		}
	} else {
		if err := unix.Mount(tmp, cgroupDir, "", uintptr(unix.MS_MOVE), ""); err != nil {
			return fmt.Errorf("failed to move mount point from %s to %s: %w", tmp, cgroupDir, err)
		}
	}
	return nil
}

func mountProcfs() error {
	if err := unix.Mount("none", "/proc", "proc", 0, ""); err != nil {
		logrus.Warnf("failed to mount procfs, falling back to read-only mount: %v", err)
		if err := unix.Mount("none", "/proc", "proc", uintptr(unix.MS_RDONLY), ""); err != nil {
			logrus.Warnf("failed to mount procfs: %v", err)
		}
	}
	return nil
}

func activateLoopback() error {
	cmds := [][]string{
		{"ip", "link", "set", "lo", "up"},
	}
	if err := common.Execs(os.Stderr, os.Environ(), cmds); err != nil {
		return fmt.Errorf("executing %v: %w", cmds, err)
	}
	return nil
}

func activateDev(dev, ip string, netmask int, gateway string, mtu int) error {
	cmds := [][]string{
		{"ip", "link", "set", dev, "up"},
		{"ip", "link", "set", "dev", dev, "mtu", strconv.Itoa(mtu)},
		{"ip", "addr", "add", ip + "/" + strconv.Itoa(netmask), "dev", dev},
		{"ip", "route", "add", "default", "via", gateway, "dev", dev},
	}
	if err := common.Execs(os.Stderr, os.Environ(), cmds); err != nil {
		return fmt.Errorf("executing %v: %w", cmds, err)
	}
	return nil
}

func setupCopyDir(driver copyup.ChildDriver, dirs []string) (bool, error) {
	if driver != nil {
		etcWasCopied := false
		copied, err := driver.CopyUp(dirs)
		for _, d := range copied {
			if d == "/etc" {
				etcWasCopied = true
				break
			}
		}
		return etcWasCopied, err
	}
	if len(dirs) != 0 {
		return false, errors.New("copy-up driver is not specified")
	}
	return false, nil
}

func setupNet(stateDir string, msg *messages.ParentInitNetworkDriverCompleted, etcWasCopied bool, driver network.ChildDriver, detachedNetNSPath string) error {
	// HostNetwork
	if driver == nil {
		return nil
	}

	if detachedNetNSPath == "" {
		// non-detached mode
		if err := activateLoopback(); err != nil {
			return err
		}
		dev, err := driver.ConfigureNetworkChild(msg, detachedNetNSPath)
		if err != nil {
			return err
		}
		if err := activateDev(dev, msg.IP, msg.Netmask, msg.Gateway, msg.MTU); err != nil {
			return err
		}
		if etcWasCopied {
			if err := writeResolvConf(msg.DNS); err != nil {
				return err
			}
			if err := writeEtcHosts(); err != nil {
				return err
			}
		} else {
			logrus.Warn("Mounting /etc/resolv.conf without copying-up /etc. " +
				"Note that /etc/resolv.conf in the namespace will be unmounted when it is recreated on the host. " +
				"Unless /etc/resolv.conf is statically configured, copying-up /etc is highly recommended. " +
				"Please refer to RootlessKit documentation for further information.")
			if err := mountResolvConf(stateDir, msg.DNS); err != nil {
				return err
			}
			if err := mountEtcHosts(stateDir); err != nil {
				return err
			}
		}
	} else {
		// detached mode
		if err := ns.WithNetNSPath(detachedNetNSPath, func(_ ns.NetNS) error {
			return activateLoopback()
		}); err != nil {
			return err
		}
		dev, err := driver.ConfigureNetworkChild(msg, detachedNetNSPath)
		if err != nil {
			return err
		}
		if err := ns.WithNetNSPath(detachedNetNSPath, func(_ ns.NetNS) error {
			return activateDev(dev, msg.IP, msg.Netmask, msg.Gateway, msg.MTU)
		}); err != nil {
			return err
		}
		// TODO: write /etc/resolv.conf and /etc/hosts in a custom directory?
	}
	return nil
}

type Opt struct {
	PipeFDEnvKey    string              // needs to be set
	StateDirEnvKey  string              // needs to be set
	TargetCmd       []string            // needs to be set
	NetworkDriver   network.ChildDriver // nil for HostNetwork
	CopyUpDriver    copyup.ChildDriver  // cannot be nil if len(CopyUpDirs) != 0
	CopyUpDirs      []string
	DetachNetNS     bool
	PortDriver      port.ChildDriver
	MountProcfs     bool   // needs to be set if (and only if) parent.Opt.CreatePIDNS is set
	Propagation     string // mount propagation type
	Reaper          bool
	EvacuateCgroup2 bool // needs to correspond to parent.Opt.EvacuateCgroup2 is set
}

// statPIDNS is from https://github.com/containerd/containerd/blob/v1.7.2/services/introspection/pidns_linux.go#L25-L36
func statPIDNS(pid int) (uint64, error) {
	f := fmt.Sprintf("/proc/%d/ns/pid", pid)
	st, err := os.Stat(f)
	if err != nil {
		return 0, err
	}
	stSys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("%T is not *syscall.Stat_t", st.Sys())
	}
	return stSys.Ino, nil
}

func hasCaps() (bool, error) {
	pid := os.Getpid()
	hdr := unix.CapUserHeader{
		Version: unix.LINUX_CAPABILITY_VERSION_3,
		Pid:     int32(pid),
	}
	var data unix.CapUserData
	if err := unix.Capget(&hdr, &data); err != nil {
		return false, fmt.Errorf("failed to get the current caps: %w", err)
	}
	logrus.Debugf("Capabilities: %+v", data)
	return data.Effective != 0, nil
}

// gainCaps gains the caps inside the user namespace.
// The caps are gained on re-execution after the child's uid_map and gid_map are fully written.
func gainCaps() error {
	pid := os.Getpid()
	pidns, err := statPIDNS(pid)
	if err != nil {
		logrus.WithError(err).Debug("Failed to stat pidns (negligible when unsharing pidns)")
		pidns = 0
	}
	envName := fmt.Sprintf("_ROOTLESSKIT_REEXEC_COUNT_%d_%d", pidns, pid)
	logrus.Debugf("Re-executing the RootlessKit child process (PID=%d) to gain the caps", pid)

	var envValueInt int
	if envValueStr := os.Getenv(envName); envValueStr != "" {
		var err error
		envValueInt, err = strconv.Atoi(envValueStr)
		if err != nil {
			return fmt.Errorf("failed to parse %s value %q: %w", envName, envValueStr, err)
		}
	}
	if envValueInt > 5 {
		time.Sleep(10 * time.Millisecond * time.Duration(envValueInt))
	}
	if envValueInt > 10 {
		return fmt.Errorf("no capabilities was gained after reexecuting the child (%s=%d)", envName, envValueInt)
	}
	logrus.Debugf("%s: %d->%d", envName, envValueInt, envValueInt+1)
	os.Setenv(envName, strconv.Itoa(envValueInt+1))

	// PID should be kept after re-execution.
	if err := syscall.Exec("/proc/self/exe", os.Args, os.Environ()); err != nil {
		return err
	}
	panic("should not reach here")
}

func Child(opt Opt) error {
	if opt.PipeFDEnvKey == "" {
		return errors.New("pipe FD env key is not set")
	}
	pipeFDStr := os.Getenv(opt.PipeFDEnvKey)
	if pipeFDStr == "" {
		return fmt.Errorf("%s is not set", opt.PipeFDEnvKey)
	}
	var pipeFD, pipe2FD int
	if _, err := fmt.Sscanf(pipeFDStr, "%d,%d", &pipeFD, &pipe2FD); err != nil {
		return fmt.Errorf("unexpected fd value: %s: %w", pipeFDStr, err)
	}
	logrus.Debugf("pipeFD=%d, pipe2FD=%d", pipeFD, pipe2FD)
	pipeR := os.NewFile(uintptr(pipeFD), "")
	pipe2W := os.NewFile(uintptr(pipe2FD), "")

	if opt.StateDirEnvKey == "" {
		opt.StateDirEnvKey = "ROOTLESSKIT_STATE_DIR" // for backward compatibility of Go API
	}
	stateDir := os.Getenv(opt.StateDirEnvKey)
	if stateDir == "" {
		return errors.New("got empty StateDir")
	}

	var (
		msg *messages.Message
		err error
	)
	if ok, err := hasCaps(); err != nil {
		return err
	} else if !ok {
		msg, err = messages.WaitFor(pipeR, messages.Name(messages.ParentHello{}))
		if err != nil {
			return err
		}

		msgChildHello := &messages.Message{
			U: messages.U{
				ChildHello: &messages.ChildHello{},
			},
		}
		if err := messages.Send(pipe2W, msgChildHello); err != nil {
			return err
		}

		msg, err = messages.WaitFor(pipeR, messages.Name(messages.ParentInitIdmapCompleted{}))
		if err != nil {
			return err
		}

		if err := gainCaps(); err != nil {
			return fmt.Errorf("failed to gain the caps inside the user namespace: %w", err)
		}
	}

	if opt.MountProcfs {
		if err := mountProcfs(); err != nil {
			return err
		}
	}

	var detachedNetNSPath string
	if opt.DetachNetNS {
		detachedNetNSPath = filepath.Join(stateDir, "netns")
		if err = NewNetNsWithPathWithoutEnter(detachedNetNSPath); err != nil {
			return fmt.Errorf("failed to create a detached netns on %q: %w", detachedNetNSPath, err)
		}
	}

	msgChildInitUserNSCompleted := &messages.Message{
		U: messages.U{
			ChildInitUserNSCompleted: &messages.ChildInitUserNSCompleted{},
		},
	}
	if err := messages.Send(pipe2W, msgChildInitUserNSCompleted); err != nil {
		return err
	}

	msg, err = messages.WaitFor(pipeR, messages.Name(messages.ParentInitNetworkDriverCompleted{}))
	if err != nil {
		return err
	}
	netMsg := msg.U.ParentInitNetworkDriverCompleted

	msg, err = messages.WaitFor(pipeR, messages.Name(messages.ParentInitPortDriverCompleted{}))
	if err != nil {
		return err
	}
	portMsg := msg.U.ParentInitPortDriverCompleted

	// The parent calls child with Pdeathsig, but it is cleared when newuidmap SUID binary is called
	// https://github.com/rootless-containers/rootlesskit/issues/65#issuecomment-492343646
	runtime.LockOSThread()
	err = unix.Prctl(unix.PR_SET_PDEATHSIG, uintptr(unix.SIGKILL), 0, 0, 0)
	runtime.UnlockOSThread()
	if err != nil {
		return err
	}
	os.Unsetenv(opt.PipeFDEnvKey)
	if err := pipeR.Close(); err != nil {
		return fmt.Errorf("failed to close fd %d: %w", pipeFD, err)
	}
	if err := setMountPropagation(opt.Propagation); err != nil {
		return err
	}
	etcWasCopied, err := setupCopyDir(opt.CopyUpDriver, opt.CopyUpDirs)
	if err != nil {
		return err
	}
	if detachedNetNSPath == "" {
		if err := mountSysfs(opt.NetworkDriver == nil, opt.EvacuateCgroup2); err != nil {
			return err
		}
	}
	if err := setupNet(stateDir, netMsg, etcWasCopied, opt.NetworkDriver, detachedNetNSPath); err != nil {
		return err
	}
	portQuitCh := make(chan struct{})
	portErrCh := make(chan error)
	if opt.PortDriver != nil {
		var portDriverOpaque map[string]string
		if portMsg != nil {
			portDriverOpaque = portMsg.PortDriverOpaque
		}
		go func() {
			portErrCh <- opt.PortDriver.RunChildDriver(portDriverOpaque, portQuitCh, detachedNetNSPath)
		}()
	}

	cmd, err := createCmd(opt.TargetCmd)
	if err != nil {
		return err
	}
	if opt.Reaper {
		if err := runAndReap(cmd); err != nil {
			return fmt.Errorf("command %v exited: %w", opt.TargetCmd, err)
		}
	} else {
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("command %v exited: %w", opt.TargetCmd, err)
		}
		sigc := sigproxy.ForwardAllSignals(context.TODO(), cmd.Process.Pid)
		defer sigproxysignal.StopCatch(sigc)
		if err := cmd.Wait(); err != nil {
			return fmt.Errorf("command %v exited: %w", opt.TargetCmd, err)
		}
	}
	if opt.PortDriver != nil {
		portQuitCh <- struct{}{}
		return <-portErrCh
	}
	return nil
}

func setMountPropagation(propagation string) error {
	flags, ok := propagationStates[propagation]
	if ok {
		if err := unix.Mount("none", "/", "", flags, ""); err != nil {
			return fmt.Errorf("failed to share mount point: /: %w", err)
		}
	}
	return nil
}

func runAndReap(cmd *exec.Cmd) error {
	c := make(chan os.Signal, 32)
	signal.Notify(c, syscall.SIGCHLD)
	cmd.SysProcAttr.Setsid = true
	if err := cmd.Start(); err != nil {
		return err
	}
	sigc := sigproxy.ForwardAllSignals(context.TODO(), cmd.Process.Pid)
	defer sigproxysignal.StopCatch(sigc)

	result := make(chan error)
	go func() {
		defer close(result)
		for cEntry := range c {
			logrus.Debugf("reaper: got signal %q", cEntry)
			if wsPtr := reap(cmd.Process.Pid); wsPtr != nil {
				ws := *wsPtr
				if ws.Exited() && ws.ExitStatus() == 0 {
					result <- nil
					continue
				}
				var resultErr common.ErrorWithSys = &reaperErr{
					ws: ws,
				}
				result <- resultErr
			}
		}
	}()
	return <-result
}

func reap(myPid int) *syscall.WaitStatus {
	var res *syscall.WaitStatus
	for {
		var ws syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
		logrus.Debugf("reaper: got ws=%+v, pid=%d, err=%+v", ws, pid, err)
		if err != nil || pid <= 0 {
			break
		}
		if pid == myPid {
			res = &ws
		}
	}
	return res
}

type reaperErr struct {
	ws syscall.WaitStatus
}

func (e *reaperErr) Sys() interface{} {
	return e.ws
}

func (e *reaperErr) Error() string {
	if e.ws.Exited() {
		return fmt.Sprintf("exit status %d", e.ws.ExitStatus())
	}
	if e.ws.Signaled() {
		return fmt.Sprintf("signal: %s", e.ws.Signal())
	}
	return fmt.Sprintf("exited with WAITSTATUS=0x%08x", e.ws)
}

func NewNetNsWithPathWithoutEnter(p string) error {
	if err := os.WriteFile(p, nil, 0400); err != nil {
		return err
	}
	// this is hard (not impossible though) to reimplement in Go: https://github.com/cloudflare/slirpnetstack/commit/d7766a8a77f0093d3cb7a94bd0ccbe3f67d411ba
	cmd := exec.Command("unshare", "-n", "mount", "--bind", "/proc/self/ns/net", p)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to execute %v: %w (out=%q)", cmd.Args, err, string(out))
	}
	return nil
}
