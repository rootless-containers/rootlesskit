package parent

import (
	"context"
	"os"
	"os/exec"
	"strconv"

	"github.com/pkg/errors"

	"github.com/AkihiroSuda/rootlesskit/pkg/common"
)

// setupVDEPlugSlirp setups network via vdeplug_slirp.
// See https://github.com/rootless-containers/runrootless/blob/f1c2e886d07b280ae1558d04cfe074aa6889a9a4/misc/vde/README.md
//
// For avoiding LGPL infection, slirp is called via vde_plug binary.
// TODO:
//  * support port forwarding
//  * use netlink
func setupVDEPlugSlirp(pid int, msg *common.Message) (func() error, error) {
	tap := "tap0"
	var cleanups []func() error
	if err := prepareTap(pid, tap); err != nil {
		return common.Seq(cleanups), errors.Wrapf(err, "setting up tap %s", tap)
	}
	slirpCtx, slirpCancel := context.WithCancel(context.Background())
	cleanups = append(cleanups, func() error { slirpCancel(); return nil })
	slirpCmd := exec.CommandContext(slirpCtx, "vde_plug", "vxvde://", "slirp://")
	if err := slirpCmd.Start(); err != nil {
		return common.Seq(cleanups), errors.Wrapf(err, "executing %v", slirpCmd)
	}

	tapCtx, tapCancel := context.WithCancel(context.Background())
	cleanups = append(cleanups, func() error { tapCancel(); return nil })
	tapCmd := exec.CommandContext(tapCtx, "vde_plug", "vxvde://",
		"=", "nsenter", "--", "-t", strconv.Itoa(pid), "-n", "-U", "--preserve-credentials",
		"vde_plug", "tap://"+tap)
	if err := tapCmd.Start(); err != nil {
		return common.Seq(cleanups), errors.Wrapf(err, "executing %v", tapCmd)
	}
	msg.Tap = tap
	// TODO: support configuration
	msg.IP = "10.0.2.100"
	msg.Netmask = 24
	msg.Gateway = "10.0.2.2"
	msg.DNS = "10.0.2.3"
	return common.Seq(cleanups), nil
}

func prepareTap(pid int, tap string) error {
	cmds := [][]string{
		nsenter(pid, []string{"ip", "tuntap", "add", "name", tap, "mode", "tap"}),
		nsenter(pid, []string{"ip", "link", "set", tap, "up"}),
	}
	if err := common.Execs(os.Stderr, os.Environ(), cmds); err != nil {
		return errors.Wrapf(err, "executing %v", cmds)
	}
	return nil
}

func nsenter(pid int, cmd []string) []string {
	pidS := strconv.Itoa(pid)
	return append([]string{"nsenter", "-t", pidS, "-n", "-m", "-U", "--preserve-credentials"}, cmd...)
}
