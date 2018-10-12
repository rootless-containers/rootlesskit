package vpnkit

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jamescun/tuntap"
	"github.com/moby/vpnkit/go/pkg/vmnet"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/rootless-containers/rootlesskit/pkg/common"
	"github.com/rootless-containers/rootlesskit/pkg/network"
)

func NewParentDriver(binary string, mtu int) network.ParentDriver {
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
	return &parentDriver{
		binary: binary,
		mtu:    mtu,
	}
}

type parentDriver struct {
	binary string
	mtu    int
}

func (d *parentDriver) NetworkMode() common.NetworkMode {
	return common.VPNKit
}

func (d *parentDriver) MTU() int {
	return d.mtu
}

func (d *parentDriver) ConfigureNetwork(childPID int, stateDir string) (*common.NetworkMessage, func() error, error) {
	var cleanups []func() error
	vpnkitSocket := filepath.Join(stateDir, "vpnkit-ethernet.sock")
	vpnkitCtx, vpnkitCancel := context.WithCancel(context.Background())
	vpnkitCmd := exec.CommandContext(vpnkitCtx, d.binary, "--ethernet", vpnkitSocket, "--mtu", strconv.Itoa(d.mtu))
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
		NetworkMode:      common.VPNKit,
		IP:               vif.IP.String(),
		Netmask:          24,
		Gateway:          "192.168.65.1",
		DNS:              "192.168.65.1",
		MTU:              d.mtu,
		PreconfiguredTap: "",
		VPNKitMAC:        vif.ClientMAC.String(),
		VPNKitSocket:     vpnkitSocket,
		VPNKitUUID:       vifUUID.String(),
	}
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

func (d *childDriver) ConfigureTap(netmsg common.NetworkMessage) (tap string, err error) {
	if netmsg.NetworkMode != common.VPNKit {
		return "", errors.Errorf("expected network mode %v, got %v", common.VPNKit, netmsg.NetworkMode)
	}
	return startVPNKitRoutines(context.TODO(),
		netmsg.VPNKitMAC, netmsg.VPNKitSocket, netmsg.VPNKitUUID)
}

func startVPNKitRoutines(ctx context.Context, macStr, socket, uuidStr string) (string, error) {
	tapName := "tap0"
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
