package bridge

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/rootless-containers/rootlesskit/v2/pkg/api"
	"github.com/rootless-containers/rootlesskit/v2/pkg/common"
	"github.com/rootless-containers/rootlesskit/v2/pkg/messages"
	"github.com/rootless-containers/rootlesskit/v2/pkg/network"
	"github.com/rootless-containers/rootlesskit/v2/pkg/network/iputils"
	"github.com/rootless-containers/rootlesskit/v2/pkg/network/parentutils"
)

func getLocalNameserver() (nameserver string) {
	ns := "1.1.1.1"
	data, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return ns
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "nameserver" {
			continue
		}
		for _, field := range fields[1:] {
			ip, err := netip.ParseAddr(field)
			if err != nil {
				continue
			}
			ns = ip.String()
			break
		}
	}

	return ns
}

func NewParentDriver(mtu int, ipnet *net.IPNet, ifname string) (network.ParentDriver, error) {
	if mtu < 0 {
		panic("got negative mtu")
	}
	if mtu == 0 {
		mtu = 1500
	}
	if ifname == "" {
		ifname = "bridge0"
	}
	if ipnet == nil {
		var err error
		_, ipnet, err = net.ParseCIDR("172.17.0.0/16")
		if err != nil {
			return nil, err
		}
	}

	return &parentDriver{
		mtu:    mtu,
		ipnet:  ipnet,
		ifname: ifname,
	}, nil

}

type parentDriver struct {
	mtu       int
	ifname    string
	ipnet     *net.IPNet
	infoMu    sync.RWMutex
	info      func() *api.NetworkDriverInfo
}

const DriverName = "bridge"

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
	bridge := d.ifname
	var cleanups []func() error
	address, err := iputils.AddIPInt(d.ipnet.IP, 2)
	if err != nil {
		return nil, common.Seq(cleanups), err
	}
	netmask, _ := d.ipnet.Mask.Size()
	gateway, err := iputils.AddIPInt(d.ipnet.IP, 1)
	if err != nil {
		return nil, common.Seq(cleanups), err
	}

	ipnet := &net.IPNet{IP: address, Mask: d.ipnet.Mask}
	if err := parentutils.PrepareBridge(childPID, detachedNetNSPath, bridge, ipnet.String(), gateway.String()); err != nil {
		return nil, common.Seq(cleanups), fmt.Errorf("setting up interface %s: %w", bridge, err)
	}

	if detachedNetNSPath != "" {
		cmd := exec.Command("nsenter", "-t", strconv.Itoa(childPID), "-n"+detachedNetNSPath, "--no-fork", "-m", "-U", "--preserve-credentials", "sleep", "infinity")
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Pdeathsig: syscall.SIGKILL,
		}
		err := cmd.Start()
		if err != nil {
			return nil, nil, err
		}
	}

	netmsg := messages.ParentInitNetworkDriverCompleted{
		Dev: bridge,
		DNS: getLocalNameserver(),
		MTU: d.mtu,
	}
	netmsg.IP = address.String()
	netmsg.Netmask = netmask
	netmsg.Gateway = gateway.String()

	d.infoMu.Lock()
	d.info = func() *api.NetworkDriverInfo {
		return &api.NetworkDriverInfo{
			Driver:         DriverName,
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
	return netmsg.Dev, nil
}
