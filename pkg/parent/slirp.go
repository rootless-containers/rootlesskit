package parent

import (
	"context"
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

	"github.com/rootless-containers/rootlesskit/pkg/common"
)

// setupVDEPlugSlirp setups network via vdeplug_slirp.
// See https://github.com/rootless-containers/runrootless/blob/f1c2e886d07b280ae1558d04cfe074aa6889a9a4/misc/vde/README.md
//
// For avoiding LGPL infection, slirp is called via vde_plug binary.
// TODO:
//  * support port forwarding
func setupVDEPlugSlirp(pid int, msg *common.Message) (func() error, error) {
	tap := "tap0"
	var cleanups []func() error
	if err := prepareTap(pid, tap); err != nil {
		return common.Seq(cleanups), errors.Wrapf(err, "setting up tap %s", tap)
	}
	socket := filepath.Join(msg.StateDir, "vdeplug-ptp.sock")
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
		return common.Seq(cleanups), errors.Wrapf(err, "executing %v", slirpCmd)
	}

	tapCtx, tapCancel := context.WithCancel(context.Background())
	tapCmd := exec.CommandContext(tapCtx, "vde_plug", socketURL,
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
	msg.PreconfiguredTap = tap
	return common.Seq(cleanups), nil
}

func setupVPNKit(pid int, msg *common.Message, vo VPNKitOpt) (func() error, error) {
	if vo.Binary == "" {
		vo.Binary = "vpnkit"
	}
	var cleanups []func() error
	vpnkitSocket := filepath.Join(msg.StateDir, "vpnkit-ethernet.sock")
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

func setupSlirp4NetNS(pid int, msg *common.Message) (func() error, error) {
	tap := "tap0"
	var cleanups []func() error
	if err := prepareTap(pid, tap); err != nil {
		return common.Seq(cleanups), errors.Wrapf(err, "setting up tap %s", tap)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "slirp4netns", strconv.Itoa(pid), tap)
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
		return common.Seq(cleanups), errors.Wrapf(err, "executing %v", cmd)
	}
	// TODO: support configuration
	msg.IP = "10.0.2.100"
	msg.Netmask = 24
	msg.Gateway = "10.0.2.2"
	msg.DNS = "10.0.2.3"
	msg.PreconfiguredTap = tap
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
