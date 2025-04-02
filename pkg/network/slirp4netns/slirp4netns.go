package slirp4netns

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"

	"github.com/rootless-containers/rootlesskit/v2/pkg/api"
	"github.com/rootless-containers/rootlesskit/v2/pkg/common"
	"github.com/rootless-containers/rootlesskit/v2/pkg/messages"
	"github.com/rootless-containers/rootlesskit/v2/pkg/network"
	"github.com/rootless-containers/rootlesskit/v2/pkg/network/iputils"
	"github.com/rootless-containers/rootlesskit/v2/pkg/network/parentutils"
)

type Features struct {
	// SupportsEnableIPv6 --enable-ipv6 (v0.2.0)
	SupportsEnableIPv6 bool
	// SupportsCIDR --cidr (v0.3.0)
	SupportsCIDR bool
	// SupportsDisableHostLoopback --disable-host-loopback (v0.3.0)
	SupportsDisableHostLoopback bool
	// SupportsAPISocket --api-socket (v0.3.0)
	SupportsAPISocket bool
	// SupportsEnableSandbox --enable-sandbox (v0.4.0)
	SupportsEnableSandbox bool
	// SupportsEnableSeccomp --enable-seccomp (v0.4.0)
	SupportsEnableSeccomp bool
	// KernelSupportsSeccomp whether the kernel supports slirp4netns --enable-seccomp
	KernelSupportsEnableSeccomp bool
}

func DetectFeatures(binary string) (*Features, error) {
	if binary == "" {
		return nil, errors.New("got empty slirp4netns binary")
	}
	realBinary, err := exec.LookPath(binary)
	if err != nil {
		return nil, fmt.Errorf("slirp4netns binary %q is not installed: %w", binary, err)
	}
	cmd := exec.Command(realBinary, "--help")
	cmd.Env = os.Environ()
	b, err := cmd.CombinedOutput()
	s := string(b)
	if err != nil {
		return nil, fmt.Errorf(
			"command \"%s --help\" failed, make sure slirp4netns v0.4.0+ is installed: %q: %w",
			realBinary, s, err,
		)
	}
	if !strings.Contains(s, "--netns-type") {
		// We don't use --netns-type, but we check the presence of --netns-type to
		// ensure slirp4netns >= v0.4.0: https://github.com/rootless-containers/rootlesskit/issues/143
		return nil, errors.New("slirp4netns seems older than v0.4.0")
	}
	kernelSupportsEnableSeccomp := false
	if unix.Prctl(unix.PR_GET_SECCOMP, 0, 0, 0, 0) != unix.EINVAL {
		kernelSupportsEnableSeccomp = unix.Prctl(unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER, 0, 0, 0) != unix.EINVAL
	}
	f := Features{
		SupportsEnableIPv6:          strings.Contains(s, "--enable-ipv6"),
		SupportsCIDR:                strings.Contains(s, "--cidr"),
		SupportsDisableHostLoopback: strings.Contains(s, "--disable-host-loopback"),
		SupportsAPISocket:           strings.Contains(s, "--api-socket"),
		SupportsEnableSandbox:       strings.Contains(s, "--enable-sandbox"),
		SupportsEnableSeccomp:       strings.Contains(s, "--enable-seccomp"),
		KernelSupportsEnableSeccomp: kernelSupportsEnableSeccomp,
	}
	return &f, nil
}

// NewParentDriver instantiates new parent driver.
// Requires slirp4netns v0.4.0 or later.
func NewParentDriver(logWriter io.Writer, binary string, mtu int, ipnet *net.IPNet, ifname string, disableHostLoopback bool, apiSocketPath string,
	enableSandbox, enableSeccomp, enableIPv6 bool) (network.ParentDriver, error) {
	if binary == "" {
		return nil, errors.New("got empty slirp4netns binary")
	}
	if mtu < 0 {
		return nil, errors.New("got negative mtu")
	}
	if mtu == 0 {
		mtu = 65520
	}

	if ifname == "" {
		ifname = "tap0"
	}

	features, err := DetectFeatures(binary)
	if err != nil {
		return nil, err
	}
	if enableIPv6 && !features.SupportsEnableIPv6 {
		return nil, errors.New("this version of slirp4netns does not support --enable-ipv6")
	}
	if ipnet != nil && !features.SupportsCIDR {
		return nil, errors.New("this version of slirp4netns does not support --cidr")
	}
	if disableHostLoopback && !features.SupportsDisableHostLoopback {
		return nil, errors.New("this version of slirp4netns does not support --disable-host-loopback")
	}
	if apiSocketPath != "" && !features.SupportsAPISocket {
		return nil, errors.New("this version of slirp4netns does not support --api-socket")
	}
	if enableSandbox && !features.SupportsEnableSandbox {
		return nil, errors.New("this version of slirp4netns does not support --enable-sandbox")
	}
	if enableSeccomp && !features.SupportsEnableSeccomp {
		return nil, errors.New("this version of slirp4netns does not support --enable-seccomp")
	}
	if enableSeccomp && !features.KernelSupportsEnableSeccomp {
		return nil, errors.New("kernel does not support seccomp")
	}

	return &parentDriver{
		logWriter:           logWriter,
		binary:              binary,
		mtu:                 mtu,
		ipnet:               ipnet,
		disableHostLoopback: disableHostLoopback,
		apiSocketPath:       apiSocketPath,
		enableSandbox:       enableSandbox,
		enableSeccomp:       enableSeccomp,
		enableIPv6:          enableIPv6,
		ifname:              ifname,
	}, nil
}

type parentDriver struct {
	logWriter           io.Writer
	binary              string
	mtu                 int
	ipnet               *net.IPNet
	disableHostLoopback bool
	apiSocketPath       string
	enableSandbox       bool
	enableSeccomp       bool
	enableIPv6          bool
	ifname              string
	infoMu              sync.RWMutex
	info                func() *api.NetworkDriverInfo
}

const DriverName = "slirp4netns"

func (d *parentDriver) Info(ctx context.Context) (*api.NetworkDriverInfo, error) {
	d.infoMu.RLock()
	infoFn := d.info
	d.infoMu.RUnlock()
	if infoFn == nil {
		return &api.NetworkDriverInfo{
			Driver: DriverName,
		}, nil
	}

	return infoFn(), nil
}

func (d *parentDriver) MTU() int {
	return d.mtu
}

func (d *parentDriver) ConfigureNetwork(childPID int, stateDir, detachedNetNSPath string) (*messages.ParentInitNetworkDriverCompleted, func() error, error) {
	if detachedNetNSPath != "" && d.enableSandbox {
		return nil, nil, errors.New("slirp4netns sandbox is not compatible with detach-netns (https://github.com/rootless-containers/slirp4netns/issues/317)")
	}
	tap := d.ifname
	var cleanups []func() error
	if err := parentutils.PrepareTap(childPID, detachedNetNSPath, tap); err != nil {
		return nil, common.Seq(cleanups), fmt.Errorf("setting up tap %s: %w", tap, err)
	}
	readyR, readyW, err := os.Pipe()
	if err != nil {
		return nil, common.Seq(cleanups), err
	}
	defer readyR.Close()
	defer readyW.Close()
	// -r: readyFD (requires slirp4netns >= v0.4.0: https://github.com/rootless-containers/rootlesskit/issues/143)
	opts := []string{"--mtu", strconv.Itoa(d.mtu), "-r", "3"}
	if d.disableHostLoopback {
		opts = append(opts, "--disable-host-loopback")
	}
	if d.ipnet != nil {
		opts = append(opts, "--cidr", d.ipnet.String())
	}
	if d.apiSocketPath != "" {
		opts = append(opts, "--api-socket", d.apiSocketPath)
	}
	if d.enableSandbox {
		opts = append(opts, "--enable-sandbox")
	}
	if d.enableSeccomp {
		opts = append(opts, "--enable-seccomp")
	}
	if d.enableIPv6 {
		opts = append(opts, "--enable-ipv6")
	}
	if detachedNetNSPath == "" {
		opts = append(opts, strconv.Itoa(childPID))
	} else {
		opts = append(opts,
			fmt.Sprintf("--userns-path=/proc/%d/ns/user", childPID),
			"--netns-type=path",
			detachedNetNSPath)
	}
	opts = append(opts, tap)

	logrus.Debugf("start %v with args: %v", d.binary, opts)
	cmd := exec.Command(d.binary, opts...)
	// FIXME: Stdout doen't seem captured
	cmd.Stdout = d.logWriter
	cmd.Stderr = d.logWriter
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}
	cmd.ExtraFiles = append(cmd.ExtraFiles, readyW)
	cleanups = append(cleanups, func() error {
		logrus.Debugf("killing slirp4netns")
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		wErr := cmd.Wait()
		logrus.Debugf("killed slirp4netns: %v", wErr)
		return nil
	})
	if err := cmd.Start(); err != nil {
		return nil, common.Seq(cleanups), fmt.Errorf("executing %v: %w", cmd, err)
	}

	if err := waitForReadyFD(cmd.Process.Pid, readyR); err != nil {
		return nil, common.Seq(cleanups), fmt.Errorf("waiting for ready fd (%v): %w", cmd, err)
	}
	netmsg := messages.ParentInitNetworkDriverCompleted{
		Dev:      tap,
		IPs:      make([]messages.NetworkDriverIP, 0, 2),
		DNS:      make([]string, 0, 2),
		Gateways: make([]string, 0, 2),
		MTU:      d.mtu,
	}
	if d.ipnet != nil {
		// TODO: get the actual configuration via slirp4netns API?
		x, err := iputils.AddIPInt(d.ipnet.IP, 100)
		if err != nil {
			return nil, common.Seq(cleanups), err
		}

		netmask, _ := d.ipnet.Mask.Size()

		netmsg.IPs = append(netmsg.IPs, messages.NetworkDriverIP{IP: x.String(), PrefixLen: netmask})
		x, err = iputils.AddIPInt(d.ipnet.IP, 2)
		if err != nil {
			return nil, common.Seq(cleanups), err
		}
		netmsg.Gateways = append(netmsg.Gateways, x.String())
		x, err = iputils.AddIPInt(d.ipnet.IP, 3)
		if err != nil {
			return nil, common.Seq(cleanups), err
		}
		netmsg.DNS = append(netmsg.DNS, x.String())
	} else {
		netmsg.IPs = append(netmsg.IPs, messages.NetworkDriverIP{IP: "10.0.2.100", PrefixLen: 24})
		netmsg.Gateways = append(netmsg.Gateways, "10.0.2.2")
		netmsg.DNS = append(netmsg.DNS, "10.0.2.3")
	}

	if d.enableIPv6 {
		// for now slirp4netns only supports fd00::3 as v6 nameserver
		// https://github.com/rootless-containers/slirp4netns/blob/ee1542e1532e6a7f266b8b6118973ab3b10a8bb5/slirp4netns.c#L272
		netmsg.DNS = append(netmsg.DNS, "fd00::3")

		// TODO(aperevalov --cidr option of slirp4netns now supports only ipv4 address
		// add ipv6 gateway
		netmsg.Gateways = append(netmsg.Gateways, "fd00::2")
		netmsg.IPs = append(netmsg.IPs, messages.NetworkDriverIP{IP: "fd00::1", PrefixLen: 64})
	}

	apiDNS := make([]net.IP, 0, cap(netmsg.DNS))
	for _, nameserver := range netmsg.DNS {
		apiDNS = append(apiDNS, net.ParseIP(nameserver))
	}

	d.infoMu.Lock()
	d.info = func() *api.NetworkDriverInfo {
		return &api.NetworkDriverInfo{
			Driver:         DriverName,
			DNS:            apiDNS,
			ChildIP:        net.ParseIP(netmsg.IPs[0].IP),
			DynamicChildIP: false,
		}
	}
	d.infoMu.Unlock()
	return &netmsg, common.Seq(cleanups), nil
}

// waitForReady is from libpod
// https://github.com/containers/libpod/blob/e6b843312b93ddaf99d0ef94a7e60ff66bc0eac8/libpod/networking_linux.go#L272-L308
func waitForReadyFD(cmdPid int, r *os.File) error {
	b := make([]byte, 16)
	for {
		if err := r.SetDeadline(time.Now().Add(1 * time.Second)); err != nil {
			return fmt.Errorf("error setting slirp4netns pipe timeout: %w", err)
		}
		if _, err := r.Read(b); err == nil {
			break
		} else {
			if os.IsTimeout(err) {
				// Check if the process is still running.
				var status syscall.WaitStatus
				pid, err := syscall.Wait4(cmdPid, &status, syscall.WNOHANG, nil)
				if err != nil {
					return fmt.Errorf("failed to read slirp4netns process status: %w", err)
				}
				if pid != cmdPid {
					continue
				}
				if status.Exited() {
					return errors.New("slirp4netns failed")
				}
				if status.Signaled() {
					return errors.New("slirp4netns killed by signal")
				}
				continue
			}
			return fmt.Errorf("failed to read from slirp4netns sync pipe: %w", err)
		}
	}
	return nil
}

func NewChildDriver() network.ChildDriver {
	return &childDriver{}
}

type childDriver struct {
}

func (d *childDriver) ChildDriverInfo() (*network.ChildDriverInfo, error) {
	return &network.ChildDriverInfo{
		ConfiguresInterface: false,
	}, nil
}

func (d *childDriver) ConfigureNetworkChild(netmsg *messages.ParentInitNetworkDriverCompleted, detachedNetNSPath string) (string, error) {
	tap := netmsg.Dev
	if tap == "" {
		return "", errors.New("could not determine the preconfigured tap")
	}
	// tap is created and "up".
	// IP stuff and MTU are not configured by the parent here,
	// and they are up to the child.
	return tap, nil
}
