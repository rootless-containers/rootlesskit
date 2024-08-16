package vpnkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/google/uuid"
	"github.com/moby/vpnkit/go/pkg/vmnet"

	"github.com/sirupsen/logrus"
	"github.com/songgao/water"

	"github.com/rootless-containers/rootlesskit/v2/pkg/api"
	"github.com/rootless-containers/rootlesskit/v2/pkg/common"
	"github.com/rootless-containers/rootlesskit/v2/pkg/messages"
	"github.com/rootless-containers/rootlesskit/v2/pkg/network"
)

func NewParentDriver(binary string, mtu int, ifname string, disableHostLoopback bool) network.ParentDriver {
	if binary == "" {
		panic("got empty vpnkit binary")
	}
	if mtu < 0 {
		panic("got negative mtu")
	}
	if mtu == 0 {
		mtu = 1500
	}
	if mtu != 1500 {
		logrus.Warnf("vpnkit is known to have issues with non-1500 MTU (current: %d), see https://github.com/rootless-containers/rootlesskit/issues/6#issuecomment-403531453", mtu)
		// NOTE: iperf3 stops working with MTU >= 16425
	}
	if ifname == "" {
		ifname = "tap0"
	}
	return &parentDriver{
		binary:              binary,
		mtu:                 mtu,
		ifname:              ifname,
		disableHostLoopback: disableHostLoopback,
	}
}

const (
	DriverName   = "vpnkit"
	opaqueMAC    = "vpnkit.mac"
	opaqueSocket = "vpnkit.socket"
	opaqueUUID   = "vpnkit.uuid"
)

type parentDriver struct {
	binary              string
	mtu                 int
	ifname              string
	disableHostLoopback bool
	infoMu              sync.RWMutex
	info                func() *api.NetworkDriverInfo
}

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
	var cleanups []func() error
	vpnkitSocket := filepath.Join(stateDir, "vpnkit-ethernet.sock")
	vpnkitCtx, vpnkitCancel := context.WithCancel(context.Background())
	vpnkitCmd := exec.CommandContext(vpnkitCtx, d.binary, "--ethernet", vpnkitSocket, "--mtu", strconv.Itoa(d.mtu))
	if d.disableHostLoopback {
		vpnkitCmd.Args = append(vpnkitCmd.Args, "--host-ip", "0.0.0.0")
	}
	vpnkitCmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}
	cleanups = append(cleanups, func() error {
		logrus.Debugf("killing vpnkit")
		vpnkitCancel()
		wErr := vpnkitCmd.Wait()
		logrus.Debugf("killed vpnkit: %v", wErr)
		return nil
	})
	if err := vpnkitCmd.Start(); err != nil {
		return nil, common.Seq(cleanups), fmt.Errorf("executing %v: %w", vpnkitCmd, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	cleanups = append(cleanups, func() error { cancel(); return nil })
	vmnet, err := waitForVPNKit(ctx, vpnkitSocket)
	if err != nil {
		return nil, common.Seq(cleanups), fmt.Errorf("connecting to %s: %w", vpnkitSocket, err)
	}
	cleanups = append(cleanups, func() error { return vmnet.Close() })
	vifUUID := uuid.New()
	logrus.Debugf("connecting to VPNKit vmnet at %s as %s", vpnkitSocket, vifUUID)
	// No context.WithTimeout..?
	vif, err := vmnet.ConnectVif(vifUUID)
	if err != nil {
		return nil, common.Seq(cleanups), fmt.Errorf("connecting to %s with uuid %s: %w", vpnkitSocket, vifUUID, err)
	}
	logrus.Debugf("connected to VPNKit vmnet")
	// TODO: support configuration
	netmsg := messages.ParentInitNetworkDriverCompleted{
		Dev:     d.ifname,
		IP:      vif.IP.String(),
		Netmask: 24,
		Gateway: "192.168.65.1",
		DNS:     []string{"192.168.65.1"},
		MTU:     d.mtu,
		NetworkDriverOpaque: map[string]string{
			opaqueMAC:    vif.ClientMAC.String(),
			opaqueSocket: vpnkitSocket,
			opaqueUUID:   vifUUID.String(),
		},
	}
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

func waitForVPNKit(ctx context.Context, socket string) (*vmnet.Vmnet, error) {
	retried := 0
	for {
		vmnet, err := vmnet.New(ctx, socket)
		if err == nil {
			return vmnet, nil
		}
		sleepTime := (retried % 100) * 10 * int(time.Microsecond)
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("last error: %v: %w", err, ctx.Err())
		case <-time.After(time.Duration(sleepTime)):
		}
		retried++
	}
}

func NewChildDriver() network.ChildDriver {
	return &childDriver{}
}

type childDriver struct {
}

func (d *childDriver) ChildDriverInfo() (*network.ChildDriverInfo, error) {
	return &network.ChildDriverInfo {
		ConfiguresInterface: false,
	}, nil
}

func (d *childDriver) ConfigureNetworkChild(netmsg *messages.ParentInitNetworkDriverCompleted, detachedNetNSPath string) (tap string, err error) {
	tapName := netmsg.Dev
	if tapName == "" {
		return "", errors.New("no dev is set")
	}
	macStr := netmsg.NetworkDriverOpaque[opaqueMAC]
	socket := netmsg.NetworkDriverOpaque[opaqueSocket]
	uuidStr := netmsg.NetworkDriverOpaque[opaqueUUID]
	if macStr == "" {
		return "", errors.New("no VPNKit MAC is set")
	}
	if socket == "" {
		return "", errors.New("no VPNKit socket is set")
	}
	if uuidStr == "" {
		return "", errors.New("no VPNKit UUID is set")
	}
	return startVPNKitRoutines(context.TODO(), tapName, macStr, socket, uuidStr, detachedNetNSPath)
}

func startVPNKitRoutines(ctx context.Context, tapName, macStr, socket, uuidStr, detachedNetNSPath string) (string, error) {
	var tap *water.Interface
	fn := func(_ ns.NetNS) error {
		cmds := [][]string{
			{"ip", "tuntap", "add", "name", tapName, "mode", "tap"},
			{"ip", "link", "set", tapName, "address", macStr},
			// IP stuff and MTU are configured in activateTap() in pkg/child/child.go
		}
		if err := common.Execs(os.Stderr, os.Environ(), cmds); err != nil {
			return fmt.Errorf("executing %v: %w", cmds, err)
		}
		var err error
		tap, err = water.New(
			water.Config{
				DeviceType: water.TAP,
				PlatformSpecificParams: water.PlatformSpecificParams{
					Name: tapName,
				},
			})
		if err != nil {
			return fmt.Errorf("creating tap %s: %w", tapName, err)
		}
		return nil
	}
	if detachedNetNSPath == "" {
		if err := fn(nil); err != nil {
			return "", err
		}
	} else {
		if err := ns.WithNetNSPath(detachedNetNSPath, fn); err != nil {
			return "", err
		}
	}
	if tap.Name() != tapName {
		return "", fmt.Errorf("expected %q, got %q", tapName, tap.Name())
	}
	vmnet, err := vmnet.New(ctx, socket)
	if err != nil {
		return "", err
	}
	vifUUID, err := uuid.Parse(uuidStr)
	if err != nil {
		return "", err
	}
	vif, err := vmnet.ConnectVif(vifUUID)
	if err != nil {
		return "", err
	}
	go tap2vif(vif, tap)
	go vif2tap(tap, vif)
	return tapName, nil
}

func tap2vif(vif *vmnet.Vif, r io.Reader) {
	b := make([]byte, 65536)
	for {
		n, err := r.Read(b)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			panic(fmt.Errorf("tap2vif: read: %w", err))
		}
		if err := vif.Write(b[:n]); err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			panic(fmt.Errorf("tap2vif: write: %w", err))
		}
	}
}

func vif2tap(w io.Writer, vif *vmnet.Vif) {
	for {
		b, err := vif.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			panic(fmt.Errorf("vif2tap: read: %w", err))
		}
		if _, err := w.Write(b); err != nil {
			if errors.Is(err, io.EOF) {
				return
			}

			panic(fmt.Errorf("vif2tap: write: %w", err))
		}
	}
}
