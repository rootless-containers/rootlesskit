package slirpnetstack

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/rootless-containers/rootlesskit/pkg/common"
	"github.com/rootless-containers/rootlesskit/pkg/network"
)

func NewParentDriver(mtu int) network.ParentDriver {
	if mtu < 0 {
		panic("got negative mtu")
	}
	if mtu == 0 {
		mtu = 65520
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
	if err := prepareTap(childPID, tap, d.mtu); err != nil {
		return nil, common.Seq(cleanups), errors.Wrapf(err, "setting up tap %s", tap)
	}
	tapCtx, tapCancel := context.WithCancel(context.Background())
	tapCmd := exec.CommandContext(tapCtx, "nsenter", "-t", strconv.Itoa(childPID), "-U", "--preserve-credentials", "-F",
		"slirpnetstack",
		"-interface", tap, "-netns", fmt.Sprintf("/proc/%d/ns/net", childPID))
	tapCmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}
	cleanups = append(cleanups, func() error {
		logrus.Debugf("killing slirpnetstack")
		tapCancel()
		wErr := tapCmd.Wait()
		logrus.Debugf("killed slirpnetstack: %v", wErr)
		return nil
	})
	if err := tapCmd.Start(); err != nil {
		return nil, common.Seq(cleanups), errors.Wrapf(err, "executing %v", tapCmd)
	}
	// TODO: support configuration
	netmsg := common.NetworkMessage{
		Dev:     tap,
		IP:      "10.0.2.100",
		Netmask: 24,
		Gateway: "10.0.2.2",
		// slirpnetstack lacks built-in DNS
		DNS: "8.8.8.8",
		MTU: d.mtu,
	}
	return &netmsg, common.Seq(cleanups), nil
}

func NewChildDriver() network.ChildDriver {
	return &childDriver{}
}

type childDriver struct {
}

func (d *childDriver) ConfigureNetworkChild(netmsg *common.NetworkMessage) (string, error) {
	// return empty interface name, because everything is already setup before starting up slirpnetstack process in the parent
	return "", nil
}

// prepareTap is copied from ../parentutils
// TODO: deduplicate
func prepareTap(pid int, tap string, mtu int) error {
	cmds := [][]string{
		nsenter(pid, []string{"ip", "link", "set", "lo", "up"}),
		nsenter(pid, []string{"ip", "tuntap", "add", "name", tap, "mode", "tap"}),
		// NOTE: MTU (and IP?) needs to be configured before launching slirpnetstack process
		nsenter(pid, []string{"ip", "link", "set", tap, "mtu", strconv.Itoa(mtu)}),
		nsenter(pid, []string{"ip", "link", "set", tap, "up"}),
		// TODO: support custom IP
		nsenter(pid, []string{"ip", "addr", "add", "10.0.2.100/24", "dev", tap}),
		nsenter(pid, []string{"ip", "route", "add", "0.0.0.0/0", "via", "10.0.2.2", "dev", tap}),
	}
	if err := common.Execs(os.Stderr, os.Environ(), cmds); err != nil {
		return errors.Wrapf(err, "executing %v", cmds)
	}
	return nil
}

func nsenter(pid int, cmd []string) []string {
	return append([]string{"nsenter", "-t", strconv.Itoa(pid), "-n", "-m", "-U", "--preserve-credentials"}, cmd...)
}
