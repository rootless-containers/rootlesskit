package child

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/google/uuid"
	"github.com/jamescun/tuntap"
	"github.com/moby/vpnkit/go/pkg/vpnkit"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/rootless-containers/rootlesskit/pkg/common"
)

func waitForParentSync(pipeFDStr string) (*common.Message, error) {
	pipeFD, err := strconv.Atoi(pipeFDStr)
	if err != nil {
		return nil, errors.Wrapf(err, "unexpected fd value: %s", pipeFDStr)
	}
	pipeR := os.NewFile(uintptr(pipeFD), "")
	hdr := make([]byte, 4)
	n, err := pipeR.Read(hdr)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read fd %d", pipeFD)
	}
	if n != 4 {
		return nil, errors.Errorf("read %d bytes, expected 4 bytes", n)
	}
	bLen := binary.LittleEndian.Uint32(hdr)
	if bLen > 1<<16 || bLen < 1 {
		return nil, errors.Errorf("bad message size: %d", bLen)
	}
	b := make([]byte, bLen)
	n, err = pipeR.Read(b)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read fd %d", pipeFD)
	}
	if n != int(bLen) {
		return nil, errors.Errorf("read %d bytes, expected %d bytes", n, bLen)
	}
	var msg common.Message
	if err := json.Unmarshal(b, &msg); err != nil {
		return nil, errors.Wrapf(err, "parsing message from fd %d: %q (length %d)", pipeFD, string(b), bLen)
	}
	if err := pipeR.Close(); err != nil {
		return nil, errors.Wrapf(err, "failed to close fd %d", pipeFD)
	}
	if msg.StateDir == "" {
		return nil, errors.New("got empty StateDir")
	}
	return &msg, nil
}

func createCmd(targetCmd []string) (*exec.Cmd, error) {
	var args []string
	if len(targetCmd) > 1 {
		args = targetCmd[1:]
	}
	cmd := exec.Command(targetCmd[0], args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}
	return cmd, nil
}

// mountSysfs is needed for mounting /sys/class/net
// when netns is unshared.
func mountSysfs() error {
	tmp, err := ioutil.TempDir("/tmp", "rksys")
	if err != nil {
		return errors.Wrap(err, "creating a directory under /tmp")
	}
	defer os.RemoveAll(tmp)
	cmds := [][]string{
		{"mount", "--rbind", "/sys/fs/cgroup", tmp},
		{"mount", "-t", "sysfs", "none", "/sys"},
		{"mount", "-n", "--move", tmp, "/sys/fs/cgroup"},
	}
	if err := common.Execs(os.Stderr, os.Environ(), cmds); err != nil {
		return errors.Wrapf(err, "executing %v", cmds)
	}
	return nil
}

func activateLoopback() error {
	cmds := [][]string{
		{"ip", "link", "set", "lo", "up"},
	}
	if err := common.Execs(os.Stderr, os.Environ(), cmds); err != nil {
		return errors.Wrapf(err, "executing %v", cmds)
	}
	return nil
}

func activateTap(tap, ip string, netmask int, gateway string, mtu int) error {
	cmds := [][]string{
		{"ip", "link", "set", tap, "up"},
		{"ip", "link", "set", "dev", tap, "mtu", strconv.Itoa(mtu)},
		{"ip", "addr", "add", ip + "/" + strconv.Itoa(netmask), "dev", tap},
		{"ip", "route", "add", "default", "via", gateway, "dev", tap},
	}
	if err := common.Execs(os.Stderr, os.Environ(), cmds); err != nil {
		return errors.Wrapf(err, "executing %v", cmds)
	}
	return nil
}

func startVPNKitRoutines(ctx context.Context, macStr, socket, uuidStr string) (string, error) {
	tapName := "tap0"
	cmds := [][]string{
		{"ip", "tuntap", "add", "name", tapName, "mode", "tap"},
		{"ip", "link", "set", tapName, "address", macStr},
		// IP stuff and MTU are configured in activateTap()
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
	vmnet, err := vpnkit.NewVmnet(ctx, socket)
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

func tap2vif(vif *vpnkit.Vif, r io.Reader) {
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

func vif2tap(w io.Writer, vif *vpnkit.Vif) {
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

func setupNet(msg *common.Message, etcWasCopied bool) error {
	if msg.NetworkMode == common.HostNetwork {
		return nil
	}
	// for /sys/class/net
	if err := mountSysfs(); err != nil {
		return err
	}
	if err := activateLoopback(); err != nil {
		return err
	}
	tap := ""
	switch msg.NetworkMode {
	case common.VDEPlugSlirp, common.Slirp4NetNS:
		tap = msg.PreconfiguredTap
	case common.VPNKit:
		var err error
		tap, err = startVPNKitRoutines(context.TODO(),
			msg.VPNKitMAC,
			msg.VPNKitSocket,
			msg.VPNKitUUID)
		if err != nil {
			return err
		}
	default:
		return errors.Errorf("invalid network mode: %+v", msg.NetworkMode)
	}
	if tap == "" {
		return errors.New("empty tap")
	}
	if err := activateTap(tap, msg.IP, msg.Netmask, msg.Gateway, msg.MTU); err != nil {
		return err
	}
	if etcWasCopied {
		if err := writeResolvConf(msg.DNS); err != nil {
			return err
		}
		if err := writeEtcHosts(); err != nil {
			return err
		}
	} else {
		logrus.Warn("Mounting /etc/resolv.conf without copying-up /etc. " +
			"Note that /etc/resolv.conf in the namespace will be unmounted when it is recreated on the host. " +
			"Unless /etc/resolv.conf is statically configured, copying-up /etc is highly recommended. " +
			"Please refer to RootlessKit documentation for further information.")
		if err := mountResolvConf(msg.StateDir, msg.DNS); err != nil {
			return err
		}
		if err := mountEtcHosts(msg.StateDir); err != nil {
			return err
		}
	}
	return nil
}

func setupCopyUp(msg *common.Message) ([]string, error) {
	switch msg.CopyUpMode {
	case common.TmpfsWithSymlinkCopyUp:
	default:
		return nil, errors.Errorf("invalid copy-up mode: %+v", msg.CopyUpMode)
	}
	// we create bind0 outside of msg.StateDir so as to allow
	// copying up /run with stateDir=/run/user/1001/rootlesskit/default.
	bind0, err := ioutil.TempDir("/tmp", "rootlesskit-b")
	if err != nil {
		return nil, errors.Wrap(err, "creating bind0 directory under /tmp")
	}
	defer os.RemoveAll(bind0)
	var copied []string
	for _, d := range msg.CopyUpDirs {
		d := filepath.Clean(d)
		if d == "/tmp" {
			// TODO: we can support copy-up /tmp by changing bind0TempDir
			return copied, errors.New("/tmp cannot be copied up")
		}
		cmds := [][]string{
			// TODO: read-only bind (does not work well for /run)
			{"mount", "--rbind", d, bind0},
			{"mount", "-n", "-t", "tmpfs", "none", d},
		}
		if err := common.Execs(os.Stderr, os.Environ(), cmds); err != nil {
			return copied, errors.Wrapf(err, "executing %v", cmds)
		}
		bind1, err := ioutil.TempDir(d, ".ro")
		if err != nil {
			return copied, errors.Wrapf(err, "creating a directory under %s", d)
		}
		cmds = [][]string{
			{"mount", "-n", "--move", bind0, bind1},
		}
		if err := common.Execs(os.Stderr, os.Environ(), cmds); err != nil {
			return copied, errors.Wrapf(err, "executing %v", cmds)
		}
		files, err := ioutil.ReadDir(bind1)
		if err != nil {
			return copied, errors.Wrapf(err, "reading dir %s", bind1)
		}
		for _, f := range files {
			fFull := filepath.Join(bind1, f.Name())
			var symlinkSrc string
			if f.Mode()&os.ModeSymlink != 0 {
				symlinkSrc, err = os.Readlink(fFull)
				if err != nil {
					return copied, errors.Wrapf(err, "reading dir %s", fFull)
				}
			} else {
				symlinkSrc = filepath.Join(filepath.Base(bind1), f.Name())
			}
			symlinkDst := filepath.Join(d, f.Name())
			if err := os.Symlink(symlinkSrc, symlinkDst); err != nil {
				return copied, errors.Wrapf(err, "symlinking %s to %s", symlinkSrc, symlinkDst)
			}
		}
		copied = append(copied, d)
	}
	return copied, nil
}

func Child(pipeFDEnvKey string, targetCmd []string) error {
	pipeFDStr := os.Getenv(pipeFDEnvKey)
	if pipeFDStr == "" {
		return errors.Errorf("%s is not set", pipeFDEnvKey)
	}
	os.Unsetenv(pipeFDEnvKey)
	msg, err := waitForParentSync(pipeFDStr)
	if err != nil {
		return err
	}
	logrus.Debugf("child: got msg from parent: %+v", msg)
	copied, err := setupCopyUp(msg)
	if err != nil {
		return err
	}
	etcWasCopied := false
	for _, d := range copied {
		if d == "/etc" {
			etcWasCopied = true
			break
		}
	}
	if err := setupNet(msg, etcWasCopied); err != nil {
		return err
	}
	cmd, err := createCmd(targetCmd)
	if err != nil {
		return err
	}
	if err := cmd.Run(); err != nil {
		return errors.Wrapf(err, "command %v exited", targetCmd)
	}
	return nil
}
