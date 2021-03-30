package vpnkit

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jamescun/tuntap"
	"github.com/moby/vpnkit/go/pkg/vmnet"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/rootless-containers/rootlesskit/pkg/api"
	"github.com/rootless-containers/rootlesskit/pkg/common"
	"github.com/rootless-containers/rootlesskit/pkg/network"
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

func (d *parentDriver) ConfigureNetwork(childPID int, stateDir string) (*common.NetworkMessage, func() error, error) {
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
		return nil, common.Seq(cleanups), errors.Wrapf(err, "executing %v", vpnkitCmd)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	cleanups = append(cleanups, func() error { cancel(); return nil })
	vmnet, err := waitForVPNKit(ctx, vpnkitSocket)
	if err != nil {
		return nil, common.Seq(cleanups), errors.Wrapf(err, "connecting to %s", vpnkitSocket)
	}
	cleanups = append(cleanups, func() error { return vmnet.Close() })
	vifUUID := uuid.New()
	logrus.Debugf("connecting to VPNKit vmnet at %s as %s", vpnkitSocket, vifUUID)
	// No context.WithTimeout..?
	vif, err := vmnet.ConnectVif(vifUUID)
	if err != nil {
		return nil, common.Seq(cleanups), errors.Wrapf(err, "connecting to %s with uuid %s", vpnkitSocket, vifUUID)
	}
	logrus.Debugf("connected to VPNKit vmnet")
	// TODO: support configuration
	netmsg := common.NetworkMessage{
		Dev:     d.ifname,
		IP:      vif.IP.String(),
		Netmask: 24,
		Gateway: "192.168.65.1",
		DNS:     "192.168.65.1",
		MTU:     d.mtu,
		Opaque: map[string]string{
			opaqueMAC:    vif.ClientMAC.String(),
			opaqueSocket: vpnkitSocket,
			opaqueUUID:   vifUUID.String(),
		},
	}
	d.infoMu.Lock()
	d.info = func() *api.NetworkDriverInfo {
		return &api.NetworkDriverInfo{
			Driver:    DriverName,
			DNS:       []net.IP{net.ParseIP(netmsg.DNS)},
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
			return nil, errors.Wrapf(ctx.Err(), "last error: %v", err)
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

func (d *childDriver) ConfigureNetworkChild(netmsg *common.NetworkMessage) (tap string, err error) {
	tapName := netmsg.Dev
	if tapName == "" {
		return "", errors.New("no dev is set")
	}
	macStr := netmsg.Opaque[opaqueMAC]
	socket := netmsg.Opaque[opaqueSocket]
	uuidStr := netmsg.Opaque[opaqueUUID]
	if macStr == "" {
		return "", errors.New("no VPNKit MAC is set")
	}
	if socket == "" {
		return "", errors.New("no VPNKit socket is set")
	}
	if uuidStr == "" {
		return "", errors.New("no VPNKit UUID is set")
	}
	return startVPNKitRoutines(context.TODO(), tapName, macStr, socket, uuidStr)
}

func startVPNKitRoutines(ctx context.Context, tapName, macStr, socket, uuidStr string) (string, error) {
	cmds := [][]string{
		{"ip", "tuntap", "add", "name", tapName, "mode", "tap"},
		{"ip", "link", "set", tapName, "address", macStr},
		// IP stuff and MTU are configured in activateTap() in pkg/child/child.go
	}
	if err := common.Execs(os.Stderr, os.Environ(), cmds); err != nil {
		return "", errors.Wrapf(err, "executing %v", cmds)
	}
	tap, err := tuntap.Tap(tapName)
	if err != nil {
		return "", errors.Wrapf(err, "creating tap %s", tapName)
	}
	if tap.Name() != tapName {
		return "", errors.Wrapf(err, "expected %q, got %q", tapName, tap.Name())
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
			panic(errors.Wrap(err, "tap2vif: read"))
		}
		if err := vif.Write(b[:n]); err != nil {
			panic(errors.Wrap(err, "tap2vif: write"))
		}
	}
}

func vif2tap(w io.Writer, vif *vmnet.Vif) {
	for {
		b, err := vif.Read()
		if err != nil {
			panic(errors.Wrap(err, "vif2tap: read"))
		}
		if _, err := w.Write(b); err != nil {
			panic(errors.Wrap(err, "vif2tap: write"))
		}
	}
}
