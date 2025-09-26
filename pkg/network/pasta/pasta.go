package pasta

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"sync"

	"github.com/sirupsen/logrus"

	"github.com/rootless-containers/rootlesskit/v3/pkg/api"
	"github.com/rootless-containers/rootlesskit/v3/pkg/common"
	"github.com/rootless-containers/rootlesskit/v3/pkg/messages"
	"github.com/rootless-containers/rootlesskit/v3/pkg/network"
	"github.com/rootless-containers/rootlesskit/v3/pkg/network/iputils"
)

type Features struct {
	// Has `--host-lo-to-ns-lo` (introduced in passt 2024_10_30.ee7d0b6)
	// https://passt.top/passt/commit/?id=b4dace8f462b346ae2135af1f8d681a99a849a5f
	HasHostLoToNsLo bool
}

func DetectFeatures(binary string) (*Features, error) {
	if binary == "" {
		return nil, errors.New("got empty pasta binary")
	}
	realBinary, err := exec.LookPath(binary)
	if err != nil {
		return nil, fmt.Errorf("pasta binary %q is not installed: %w", binary, err)
	}
	cmd := exec.Command(realBinary, "--version")
	b, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf(`command "%s --version" failed, make sure pasta is installed: %q: %w`,
			realBinary, string(b), err)
	}
	f := Features{
		HasHostLoToNsLo: false,
	}
	cmd = exec.Command(realBinary, "--host-lo-to-ns-lo", "--version")
	if cmd.Run() == nil {
		f.HasHostLoToNsLo = true
	}
	return &f, nil
}

// NewParentDriver instantiates new parent driver.
func NewParentDriver(logWriter io.Writer, binary string, mtu int, ipnet *net.IPNet, ifname string,
	disableHostLoopback, enableIPv6, implicitPortForwarding bool) (network.ParentDriver, error) {
	if binary == "" {
		return nil, errors.New("got empty slirp4netns binary")
	}
	if mtu < 0 {
		return nil, errors.New("got negative mtu")
	}
	if mtu == 0 {
		mtu = 65520
	}

	if ipnet == nil {
		var err error
		_, ipnet, err = net.ParseCIDR("10.0.2.0/24")
		if err != nil {
			return nil, err
		}
	}

	if ifname == "" {
		ifname = "tap0"
	}

	feat, err := DetectFeatures(binary)
	if err != nil {
		return nil, err
	}

	return &parentDriver{
		logWriter:              logWriter,
		binary:                 binary,
		mtu:                    mtu,
		ipnet:                  ipnet,
		disableHostLoopback:    disableHostLoopback,
		enableIPv6:             enableIPv6,
		ifname:                 ifname,
		implicitPortForwarding: implicitPortForwarding,
		feat:                   feat,
	}, nil
}

type parentDriver struct {
	logWriter              io.Writer
	binary                 string
	mtu                    int
	ipnet                  *net.IPNet
	disableHostLoopback    bool
	enableIPv6             bool
	ifname                 string
	infoMu                 sync.RWMutex
	implicitPortForwarding bool
	info                   func() *api.NetworkDriverInfo
	feat                   *Features
}

const DriverName = "pasta"

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
	tap := d.ifname
	var cleanups []func() error

	address, err := iputils.AddIPInt(d.ipnet.IP, 100)
	if err != nil {
		return nil, common.Seq(cleanups), err
	}
	netmask, _ := d.ipnet.Mask.Size()
	gateway, err := iputils.AddIPInt(d.ipnet.IP, 2)
	if err != nil {
		return nil, common.Seq(cleanups), err
	}
	dns, err := iputils.AddIPInt(d.ipnet.IP, 3)
	if err != nil {
		return nil, common.Seq(cleanups), err
	}

	opts := []string{
		"--stderr",
		"--ns-ifname=" + d.ifname,
		"--mtu=" + strconv.Itoa(d.mtu),
		"--config-net",
		"--address=" + address.String(),
		"--netmask=" + strconv.Itoa(netmask),
		"--gateway=" + gateway.String(),
		"--dns-forward=" + dns.String(),
	}
	if d.disableHostLoopback {
		opts = append(opts, "--no-map-gw")
	}
	if !d.enableIPv6 {
		opts = append(opts, "--ipv4-only")
	}
	if d.implicitPortForwarding {
		opts = append(opts, "--tcp-ports=auto",
			"--udp-ports=auto")
	} else {
		opts = append(opts, "--tcp-ports=none",
			"--udp-ports=none")
	}
	if d.feat != nil {
		if d.feat.HasHostLoToNsLo {
			// Needed to keep `docker run -p 127.0.0.1:8080:80` functional with
			// passt >= 2024_10_30.ee7d0b6
			//
			// https://github.com/rootless-containers/rootlesskit/pull/482#issuecomment-2591798590
			opts = append(opts, "--host-lo-to-ns-lo")
		}
	}
	if detachedNetNSPath == "" {
		opts = append(opts, strconv.Itoa(childPID))
	} else {
		opts = append(opts,
			fmt.Sprintf("--userns=/proc/%d/ns/user", childPID),
			"--netns="+detachedNetNSPath)
	}

	// FIXME: Doesn't work with:
	// - passt-0.0~git20230627.289301b-1 (Ubuntu 23.10)
	// - passt-0.0~git20240220.1e6f92b-1 (Ubuntu 24.04)
	// see https://bugs.launchpad.net/ubuntu/+source/passt/+bug/2077158
	//
	// Workaround: set the kernel.apparmor_restrict_unprivileged_userns
	// sysctl to 0, or (preferred) add the AppArmor profile from upstream,
	// or from Debian packages, or from Ubuntu > 24.10.
	cmd := exec.Command(d.binary, opts...)
	logrus.Debugf("Executing %v", cmd.Args)
	out, err := cmd.CombinedOutput()
	if err != nil {
		exitErr := &exec.ExitError{}
		if errors.As(err, &exitErr) {
			return nil, common.Seq(cleanups),
				fmt.Errorf("pasta failed with exit code %d:\n%s",
					exitErr.ExitCode(), string(out))
		}
		return nil, common.Seq(cleanups), fmt.Errorf("executing %v: %w", cmd, err)
	}

	netmsg := messages.ParentInitNetworkDriverCompleted{
		Dev: tap,
		MTU: d.mtu,
	}
	netmsg.IP = address.String()
	netmsg.Netmask = netmask
	netmsg.Gateway = gateway.String()
	netmsg.DNS = []string{dns.String()}

	d.infoMu.Lock()
	d.info = func() *api.NetworkDriverInfo {
		return &api.NetworkDriverInfo{
			Driver:         DriverName,
			DNS:            []net.IP{net.ParseIP(netmsg.DNS[0])},
			ChildIP:        net.ParseIP(netmsg.IP),
			DynamicChildIP: false,
		}
	}
	d.infoMu.Unlock()
	return &netmsg, common.Seq(cleanups), nil
}

func NewChildDriver() network.ChildDriver {
	return &childDriver{}
}

type childDriver struct {
}

func (d *childDriver) ChildDriverInfo() (*network.ChildDriverInfo, error) {
	return &network.ChildDriverInfo{
		ConfiguresInterface: true,
	}, nil
}

func (d *childDriver) ConfigureNetworkChild(netmsg *messages.ParentInitNetworkDriverCompleted, detachedNetNSPath string) (string, error) {
	// NOP
	return netmsg.Dev, nil
}
