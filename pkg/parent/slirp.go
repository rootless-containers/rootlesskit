package parent

import (
	"context"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/moby/vpnkit/go/pkg/vpnkit"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

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
	slirpCmd := exec.CommandContext(slirpCtx, "vde_plug", "vxvde://", "slirp://")
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
		return common.Seq(cleanups), errors.Wrapf(err, "executing %v", slirpCmd)
	}

	tapCtx, tapCancel := context.WithCancel(context.Background())
	tapCmd := exec.CommandContext(tapCtx, "vde_plug", "vxvde://",
		"=", "nsenter", "--", "-t", strconv.Itoa(pid), "-n", "-U", "--preserve-credentials",
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
		return common.Seq(cleanups), errors.Wrapf(err, "executing %v", tapCmd)
	}
	// TODO: support configuration
	msg.IP = "10.0.2.100"
	msg.Netmask = 24
	msg.Gateway = "10.0.2.2"
	msg.DNS = "10.0.2.3"
	msg.VDEPlugTap = tap
	return common.Seq(cleanups), nil
}

func setupVPNKit(pid int, msg *common.Message, vo VPNKitOpt) (func() error, error) {
	if vo.Binary == "" {
		vo.Binary = "vpnkit"
	}
	var cleanups []func() error
	tempDir, err := ioutil.TempDir("", "rootlesskit-vpnkit")
	if err != nil {
		return common.Seq(cleanups), errors.Wrapf(err, "creating %s", tempDir)
	}
	cleanups = append(cleanups, func() error { return os.RemoveAll(tempDir) })
	vpnkitSocket := filepath.Join(tempDir, "socket")
	vpnkitCtx, vpnkitCancel := context.WithCancel(context.Background())
	vpnkitCmd := exec.CommandContext(vpnkitCtx, vo.Binary, "--ethernet", vpnkitSocket)
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
		return common.Seq(cleanups), errors.Wrapf(err, "executing %v", vpnkitCmd)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	cleanups = append(cleanups, func() error { cancel(); return nil })
	vmnet, err := waitForVPNKit(ctx, vpnkitSocket)
	if err != nil {
		return common.Seq(cleanups), errors.Wrapf(err, "connecting to %s", vpnkitSocket)
	}
	cleanups = append(cleanups, func() error { return vmnet.Close() })
	vifUUID := uuid.New()
	vif, err := vmnet.ConnectVif(vifUUID)
	if err != nil {
		return common.Seq(cleanups), errors.Wrapf(err, "connecting to %s with uuid %s", vpnkitSocket, vifUUID)
	}
	// TODO: support configuration
	msg.IP = vif.IP.String()
	msg.Netmask = 24
	msg.Gateway = "192.168.65.1"
	msg.DNS = "192.168.65.1"
	msg.VPNKitMAC = vif.ClientMAC.String()
	msg.VPNKitSocket = vpnkitSocket
	msg.VPNKitUUID = vifUUID.String()
	return common.Seq(cleanups), nil
}

func waitForVPNKit(ctx context.Context, socket string) (*vpnkit.Vmnet, error) {
	retried := 0
	for {
		vmnet, err := vpnkit.NewVmnet(ctx, socket)
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
