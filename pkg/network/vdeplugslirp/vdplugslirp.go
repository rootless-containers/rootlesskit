package vdeplugslirp

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/rootless-containers/rootlesskit/pkg/common"
	"github.com/rootless-containers/rootlesskit/pkg/network"
	"github.com/rootless-containers/rootlesskit/pkg/network/parentutils"
)

func NewParentDriver(mtu int) network.ParentDriver {
	if mtu < 0 {
		panic("got negative mtu")
	}
	if mtu == 0 {
		mtu = 1500
	}
	if mtu != 1500 {
		logrus.Warnf("vdeplug_slirp does not support non-1500 MTU, got %d", mtu)
		// TAP will be configured with the specified MTU (by the child),
		// but the specified MTU cannot be passed to vdeplug_slirp.
	}
	return &parentDriver{
		mtu: mtu,
	}
}

type parentDriver struct {
	mtu int
}

func (d *parentDriver) MTU() int {
	return d.mtu
}

func (d *parentDriver) ConfigureNetwork(childPID int, stateDir string) (*common.NetworkMessage, func() error, error) {
	tap := "tap0"
	var cleanups []func() error
	if err := parentutils.PrepareTap(childPID, tap); err != nil {
		return nil, common.Seq(cleanups), errors.Wrapf(err, "setting up tap %s", tap)
	}
	socket := filepath.Join(stateDir, "vdeplug-ptp.sock")
	socketURL := "ptp://" + socket
	slirpCtx, slirpCancel := context.WithCancel(context.Background())
	slirpCmd := exec.CommandContext(slirpCtx, "vde_plug", "slirp://", socketURL)
	slirpCmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}
	cleanups = append(cleanups, func() error {
		logrus.Debugf("killing vde_plug(slirp)")
		slirpCancel()
		wErr := slirpCmd.Wait()
		logrus.Debugf("killed vde_plug(slirp): %v", wErr)
		return nil
	})
	if err := slirpCmd.Start(); err != nil {
		return nil, common.Seq(cleanups), errors.Wrapf(err, "executing %v", slirpCmd)
	}

	tapCtx, tapCancel := context.WithCancel(context.Background())
	tapCmd := exec.CommandContext(tapCtx, "vde_plug", socketURL,
		"=", "nsenter", "--", "-t", strconv.Itoa(childPID), "-n", "-U", "--preserve-credentials",
		"vde_plug", "tap://"+tap)
	tapCmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}
	cleanups = append(cleanups, func() error {
		logrus.Debugf("killing vde_plug(tap)")
		tapCancel()
		wErr := tapCmd.Wait()
		logrus.Debugf("killed vde_plug(tap): %v", wErr)
		return nil
	})
	if err := tapCmd.Start(); err != nil {
		return nil, common.Seq(cleanups), errors.Wrapf(err, "executing %v", tapCmd)
	}
	// TODO: support configuration
	netmsg := common.NetworkMessage{
		IP:               "10.0.2.100",
		Netmask:          24,
		Gateway:          "10.0.2.2",
		DNS:              "10.0.2.3",
		MTU:              d.mtu,
		PreconfiguredTap: tap,
	}
	return &netmsg, common.Seq(cleanups), nil
}

func NewChildDriver() network.ChildDriver {
	return &childDriver{}
}

type childDriver struct {
}

func (d *childDriver) ConfigureTap(netmsg common.NetworkMessage) (tap string, err error) {
	if netmsg.PreconfiguredTap == "" {
		return "", errors.New("could not determine the preconfigured tap")
	}
	return netmsg.PreconfiguredTap, nil
}
