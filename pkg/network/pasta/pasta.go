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

	"github.com/rootless-containers/rootlesskit/pkg/api"
	"github.com/rootless-containers/rootlesskit/pkg/common"
	"github.com/rootless-containers/rootlesskit/pkg/messages"
	"github.com/rootless-containers/rootlesskit/pkg/network"
	"github.com/rootless-containers/rootlesskit/pkg/network/iputils"
	"github.com/rootless-containers/rootlesskit/pkg/network/parentutils"
)

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

	return &parentDriver{
		logWriter:              logWriter,
		binary:                 binary,
		mtu:                    mtu,
		ipnet:                  ipnet,
		disableHostLoopback:    disableHostLoopback,
		enableIPv6:             enableIPv6,
		ifname:                 ifname,
		implicitPortForwarding: implicitPortForwarding,
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
	if err := parentutils.PrepareTap(childPID, detachedNetNSPath, tap); err != nil {
		return nil, common.Seq(cleanups), fmt.Errorf("setting up tap %s: %w", tap, err)
	}

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
		"--foreground",
		"--stderr",
		"--ns-ifname=" + d.ifname,
		"--mtu=" + strconv.Itoa(d.mtu),
		"--no-dhcp",
		"--no-ra",
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
		// TCP ports are periodically watched, but UDP ports are not.
	} else {
		opts = append(opts, "--tcp-ports=none",
			"--udp-ports=none")
	}
	if detachedNetNSPath == "" {
		opts = append(opts, strconv.Itoa(childPID))
	} else {
		opts = append(opts,
			fmt.Sprintf("--userns=/proc/%d/ns/user", childPID),
			"--netns="+detachedNetNSPath)
	}

	// FIXME: Doesn't work with passt_0.0~git20230216.4663ccc-1_amd64.deb (Ubuntu 23.04)
	// `Couldn't open user namespace /proc/51813/ns/user: Permission denied`
	// Possibly related to AppArmor.
	cmd := exec.Command(d.binary, opts...)
	cmd.Stdout = d.logWriter
	cmd.Stderr = d.logWriter
	cleanups = append(cleanups, func() error {
		logrus.Debugf("killing pasta")
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		wErr := cmd.Wait()
		logrus.Debugf("killed pasta: %v", wErr)
		return nil
	})
	if err := cmd.Start(); err != nil {
		return nil, common.Seq(cleanups), fmt.Errorf("executing %v: %w", cmd, err)
	}
	netmsg := messages.ParentInitNetworkDriverCompleted{
		Dev: tap,
		MTU: d.mtu,
	}
	netmsg.IP = address.String()
	netmsg.Netmask = netmask
	netmsg.Gateway = gateway.String()
	netmsg.DNS = dns.String()

	d.infoMu.Lock()
	d.info = func() *api.NetworkDriverInfo {
		return &api.NetworkDriverInfo{
			Driver:         DriverName,
			DNS:            []net.IP{net.ParseIP(netmsg.DNS)},
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

func (d *childDriver) ConfigureNetworkChild(netmsg *messages.ParentInitNetworkDriverCompleted, detachedNetNSPath string) (string, error) {
	// NOP
	return netmsg.Dev, nil
}
