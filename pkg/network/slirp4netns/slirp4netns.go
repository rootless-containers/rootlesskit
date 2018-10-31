package slirp4netns

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/rootless-containers/rootlesskit/pkg/common"
	"github.com/rootless-containers/rootlesskit/pkg/network"
	"github.com/rootless-containers/rootlesskit/pkg/network/parentutils"
)

func NewParentDriver(binary string, mtu int) network.ParentDriver {
	if binary == "" {
		panic("got empty slirp4netns binary")
	}
	if mtu < 0 {
		panic("got negative mtu")
	}
	if mtu == 0 {
		mtu = 65520
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

func (d *parentDriver) MTU() int {
	return d.mtu
}

func (d *parentDriver) ConfigureNetwork(childPID int, stateDir string) (*common.NetworkMessage, func() error, error) {
	tap := "tap0"
	var cleanups []func() error
	if err := parentutils.PrepareTap(childPID, tap); err != nil {
		return nil, common.Seq(cleanups), errors.Wrapf(err, "setting up tap %s", tap)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, d.binary, "--mtu", strconv.Itoa(d.mtu), strconv.Itoa(childPID), tap)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}
	cleanups = append(cleanups, func() error {
		logrus.Debugf("killing slirp4netns")
		cancel()
		wErr := cmd.Wait()
		logrus.Debugf("killed slirp4netns: %v", wErr)
		return nil
	})
	if err := cmd.Start(); err != nil {
		return nil, common.Seq(cleanups), errors.Wrapf(err, "executing %v", cmd)
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
