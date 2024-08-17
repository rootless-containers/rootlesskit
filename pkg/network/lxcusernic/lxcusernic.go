package lxcusernic

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/client4"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/rootless-containers/rootlesskit/v2/pkg/api"
	"github.com/rootless-containers/rootlesskit/v2/pkg/common"
	"github.com/rootless-containers/rootlesskit/v2/pkg/messages"
	"github.com/rootless-containers/rootlesskit/v2/pkg/network"
	"github.com/sirupsen/logrus"
)

func NewParentDriver(binary string, mtu int, bridge, ifname string) (network.ParentDriver, error) {
	if binary == "" {
		return nil, errors.New("got empty binary")
	}
	if mtu < 0 {
		return nil, errors.New("got negative mtu")
	}
	if mtu == 0 {
		mtu = 1500
	}
	if bridge == "" {
		return nil, errors.New("got empty bridge")
	}
	if ifname == "" {
		ifname = "eth0"
	}
	return &parentDriver{
		binary: binary,
		mtu:    mtu,
		bridge: bridge,
		ifname: ifname,
	}, nil
}

type parentDriver struct {
	binary string
	mtu    int
	bridge string
	ifname string
}

const DriverName = "lxc-user-nic"

func (d *parentDriver) Info(ctx context.Context) (*api.NetworkDriverInfo, error) {
	return &api.NetworkDriverInfo{
		Driver: DriverName,
		// TODO: fill DNS
		// TODO: fill IP
		DynamicChildIP: true,
	}, nil
}

func (d *parentDriver) MTU() int {
	return d.mtu
}

func (d *parentDriver) ConfigureNetwork(childPID int, stateDir, detachedNetNSPath string) (*messages.ParentInitNetworkDriverCompleted, func() error, error) {
	if detachedNetNSPath != "" {
		cmd := exec.Command("nsenter", "-t", strconv.Itoa(childPID), "-n"+detachedNetNSPath, "--no-fork", "-m", "-U", "--preserve-credentials", "sleep", "infinity")
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Pdeathsig: syscall.SIGKILL,
		}
		err := cmd.Start()
		if err != nil {
			return nil, nil, err
		}
		childPID = cmd.Process.Pid
	}
	var cleanups []func() error
	dummyLXCPath := "/dev/null"
	dummyLXCName := "dummy"
	cmd := exec.Command(d.binary, "create", dummyLXCPath, dummyLXCName, strconv.Itoa(childPID), "veth", d.bridge, d.ifname)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return nil, common.Seq(cleanups), fmt.Errorf("%s failed: %s: %w", d.binary, string(b), err)
	}
	netmsg := messages.ParentInitNetworkDriverCompleted{
		Dev: d.ifname,
		// IP, Netmask, Gateway, and DNS are configured in Child (via DHCP)
		MTU: d.mtu,
	}
	return &netmsg, common.Seq(cleanups), nil
}

func NewChildDriver() network.ChildDriver {
	return &childDriver{}
}

type childDriver struct {
}

func exchangeDHCP(c *client4.Client, dev string, detachedNetNSPath string) (*dhcpv4.DHCPv4, error) {
	logrus.Debugf("exchanging DHCP messages using %s, may take a few seconds", dev)
	var (
		ps  []*dhcpv4.DHCPv4
		err error
	)
	exchange := func(ns.NetNS) error {
		for {
			ps, err = c.Exchange(dev)
			if err != nil {
				// `github.com/insomniacslk/dhcp` does not use errors.Wrap,
				// so we need to compare the string.
				if strings.Contains(err.Error(), "interrupted system call") {
					// Retry on EINTR
					continue
				}
				return fmt.Errorf("could not exchange DHCP with %s: %w", dev, err)
			}
			return nil
		}
	}
	nsPath := "/proc/self/ns/net"
	if detachedNetNSPath != "" {
		nsPath = detachedNetNSPath
	}
	if err := ns.WithNetNSPath(nsPath, exchange); err != nil {
		return nil, err
	}
	if len(ps) < 1 {
		return nil, errors.New("got empty DHCP message")
	}
	var ack *dhcpv4.DHCPv4
	for i, p := range ps {
		logrus.Debugf("DHCP message %d: %s", i, p.Summary())
		if p.MessageType() == dhcpv4.MessageTypeAck {
			ack = p
		}
	}
	if ack == nil {
		return nil, errors.New("did not get DHCPACK")
	}
	return ack, nil
}

func (d *childDriver) ChildDriverInfo() (*network.ChildDriverInfo, error) {
	return &network.ChildDriverInfo {
		ConfiguresInterface: false,
	}, nil
}

func (d *childDriver) ConfigureNetworkChild(netmsg *messages.ParentInitNetworkDriverCompleted, detachedNetNSPath string) (string, error) {
	dev := netmsg.Dev
	if dev == "" {
		return "", errors.New("could not determine the dev")
	}
	nsPath := "/proc/self/ns/net"
	if detachedNetNSPath != "" {
		nsPath = detachedNetNSPath
	}
	cmds := [][]string{
		// FIXME(AkihiroSuda): this should be moved to pkg/child?
		{"nsenter", "-n" + nsPath, "ip", "link", "set", dev, "up"},
	}
	if err := common.Execs(os.Stderr, os.Environ(), cmds); err != nil {
		return "", fmt.Errorf("executing %v: %w", cmds, err)
	}
	c := client4.NewClient()
	c.ReadTimeout = 30 * time.Second
	c.WriteTimeout = 30 * time.Second
	p, err := exchangeDHCP(c, dev, detachedNetNSPath)
	if err != nil {
		return "", err
	}
	if p.YourIPAddr.Equal(net.IPv4zero) {
		return "", errors.New("got zero YourIPAddr")
	}
	if len(p.Router()) == 0 {
		return "", errors.New("got no Router")
	}
	if len(p.DNS()) == 0 {
		return "", errors.New("got no DNS")
	}
	netmsg.IP = p.YourIPAddr.To4().String()
	netmask, _ := p.SubnetMask().Size()
	netmsg.Netmask = netmask
	netmsg.Gateway = p.Router()[0].To4().String()
	netmsg.DNS = []string{p.DNS()[0].To4().String()}
	go dhcpRenewRoutine(c, dev, p.YourIPAddr.To4(), p.IPAddressLeaseTime(time.Hour), detachedNetNSPath)
	return dev, nil
}

func dhcpRenewRoutine(c *client4.Client, dev string, initialIP net.IP, lease time.Duration, detachedNetNSPath string) {
	for {
		if lease <= 0 {
			return
		}
		logrus.Debugf("DHCP lease=%s, sleeping lease * 0.9", lease)
		time.Sleep(time.Duration(float64(lease) * 0.9))
		p, err := exchangeDHCP(c, dev, detachedNetNSPath)
		if err != nil {
			panic(err)
		}
		ip := p.YourIPAddr.To4()
		if !ip.Equal(initialIP) {
			// FIXME(AkihiroSuda): unlikely to happen for LXC usecase but good to consider supporting
			panic(fmt.Errorf("expected to retain %s, got %s", initialIP, ip))
		}
		lease = p.IPAddressLeaseTime(lease)
	}
}
