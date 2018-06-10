package parent

import (
	"context"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/AkihiroSuda/rootlesskit/pkg/util"
)

// setupVDEPlugSlirp setups network via vdeplug_slirp.
// See https://github.com/rootless-containers/runrootless/blob/f1c2e886d07b280ae1558d04cfe074aa6889a9a4/misc/vde/README.md
//
// For avoiding LGPL infection, slirp is called via vde_plug binary.
// TODO:
//  * support port forwarding
//  * use netlink
func setupVDEPlugSlirp(pid int) (func() error, error) {
	const (
		tap     = "tap0"
		ip      = "10.0.2.100"
		netmask = "24"
		gateway = "10.0.2.2"
		dns     = "10.0.2.3"
	)
	logrus.Debugf("vdeplug_slirp: tap=%s, ip=%s/%s, gateway=%s, dns=%s", tap, ip, netmask, gateway, dns)
	var cleanups []func() error
	cmds := [][]string{
		nsenter(pid, []string{"ip", "tuntap", "add", "name", tap, "mode", "tap"}),
		nsenter(pid, []string{"ip", "link", "set", tap, "up"}),
	}
	if err := util.Execs(os.Stderr, os.Environ(), cmds); err != nil {
		return cleanup(cleanups), errors.Wrapf(err, "executing %v", cmds)
	}

	slirpCtx, slirpCancel := context.WithCancel(context.Background())
	cleanups = append(cleanups, func() error { slirpCancel(); return nil })
	slirpCmd := exec.CommandContext(slirpCtx, "vde_plug", "vxvde://", "slirp://")
	if err := slirpCmd.Start(); err != nil {
		return cleanup(cleanups), errors.Wrapf(err, "executing %v", slirpCmd)
	}

	tapCtx, tapCancel := context.WithCancel(context.Background())
	cleanups = append(cleanups, func() error { tapCancel(); return nil })
	tapCmd := exec.CommandContext(tapCtx, "vde_plug", "vxvde://",
		"=", "nsenter", "--", "-t", strconv.Itoa(pid), "-n", "-U", "--preserve-credentials",
		"vde_plug", "tap://"+tap)
	if err := tapCmd.Start(); err != nil {
		return cleanup(cleanups), errors.Wrapf(err, "executing %v", tapCmd)
	}

	tempDir, err := ioutil.TempDir("", "rootlesskit-slirp")
	if err != nil {
		return cleanup(cleanups), errors.Wrapf(err, "creating %s", tempDir)
	}
	cleanups = append(cleanups, func() error { return os.RemoveAll(tempDir) })
	resolvConf := filepath.Join(tempDir, "resolv.conf")
	if err := ioutil.WriteFile(resolvConf, []byte("nameserver "+dns), 0644); err != nil {
		return cleanup(cleanups), errors.Wrapf(err, "writing %s", resolvConf)
	}
	cmds = [][]string{
		nsenter(pid, []string{"ip", "link", "set", tap, "up"}),
		nsenter(pid, []string{"ip", "addr", "add", ip + "/" + netmask, "dev", tap}),
		nsenter(pid, []string{"ip", "route", "add", "default", "via", gateway, "dev", tap}),
		nsenter(pid, []string{"mount", "--bind", resolvConf, "/etc/resolv.conf"}),
	}
	if err := util.Execs(os.Stderr, os.Environ(), cmds); err != nil {
		return cleanup(cleanups), errors.Wrapf(err, "executing %v", cmds)
	}
	return cleanup(cleanups), nil
}

func nsenter(pid int, cmd []string) []string {
	pidS := strconv.Itoa(pid)
	return append([]string{"nsenter", "-t", pidS, "-n", "-m", "-U", "--preserve-credentials"}, cmd...)
}

func cleanup(cleanups []func() error) func() error {
	return func() error {
		for _, c := range cleanups {
			if cerr := c(); cerr != nil {
				return cerr
			}
		}
		return nil
	}
}
