package lxcusernic

import (
	"context"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/client4"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/rootless-containers/rootlesskit/pkg/api"
	"github.com/rootless-containers/rootlesskit/pkg/common"
	"github.com/rootless-containers/rootlesskit/pkg/network"
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

func (d *parentDriver) ConfigureNetwork(childPID int, stateDir string) (*common.NetworkMessage, func() error, error) {
	var cleanups []func() error
	dummyLXCPath := "/dev/null"
	dummyLXCName := "dummy"
	cmd := exec.Command(d.binary, "create", dummyLXCPath, dummyLXCName, strconv.Itoa(childPID), "veth", d.bridge, d.ifname)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return nil, common.Seq(cleanups), errors.Wrapf(err, "%s failed: %s", d.binary, string(b))
	}
	netmsg := common.NetworkMessage{
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

func exchangeDHCP(c *client4.Client, dev string) (*dhcpv4.DHCPv4, error) {
	logrus.Debugf("exchanging DHCP messages using %s, may take a few seconds", dev)
	var (
		ps  []*dhcpv4.DHCPv4
		err error
	)
	for {
		ps, err = c.Exchange(dev)
		if err != nil {
			// `github.com/insomniacslk/dhcp` does not use errors.Wrap,
			// so we need to compare the string.
			if strings.Contains(err.Error(), "interrupted system call") {
				// Retry on EINTR
				continue
			}
			return nil, errors.Wrapf(err, "could not exchange DHCP with %s", dev)
		}
		break
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

func (d *childDriver) ConfigureNetworkChild(netmsg *common.NetworkMessage) (string, error) {
	dev := netmsg.Dev
	if dev == "" {
		return "", errors.New("could not determine the dev")
	}
	cmds := [][]string{
		// FIXME(AkihiroSuda): this should be moved to pkg/child?
		{"ip", "link", "set", dev, "up"},
	}
	if err := common.Execs(os.Stderr, os.Environ(), cmds); err != nil {
		return "", errors.Wrapf(err, "executing %v", cmds)
	}
	c := client4.NewClient()
	c.ReadTimeout = 30 * time.Second
	c.WriteTimeout = 30 * time.Second
	p, err := exchangeDHCP(c, dev)
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
	netmsg.DNS = p.DNS()[0].To4().String()
	go dhcpRenewRoutine(c, dev, p.YourIPAddr.To4(), p.IPAddressLeaseTime(time.Hour))
	return dev, nil
}

func dhcpRenewRoutine(c *client4.Client, dev string, initialIP net.IP, lease time.Duration) {
	for {
		if lease <= 0 {
			return
		}
		logrus.Debugf("DHCP lease=%s, sleeping lease * 0.9", lease)
		time.Sleep(time.Duration(float64(lease) * 0.9))
		p, err := exchangeDHCP(c, dev)
		if err != nil {
			panic(err)
		}
		ip := p.YourIPAddr.To4()
		if !ip.Equal(initialIP) {
			// FIXME(AkihiroSuda): unlikely to happen for LXC usecase but good to consider supporting
			panic(errors.Errorf("expected to retain %s, got %s", initialIP, ip))
		}
		lease = p.IPAddressLeaseTime(lease)
	}
}
