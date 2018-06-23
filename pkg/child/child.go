package child

import (
	"encoding/binary"
	"encoding/json"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/AkihiroSuda/rootlesskit/pkg/common"
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
	return cmd, nil
}

func activateTap(tap, ip string, netmask int, gateway, dns string) (func() error, error) {
	var cleanups []func() error
	tempDir, err := ioutil.TempDir("", "rootlesskit-dns")
	if err != nil {
		return common.Seq(cleanups), errors.Wrapf(err, "creating %s", tempDir)
	}
	cleanups = append(cleanups, func() error { return os.RemoveAll(tempDir) })
	resolvConf := filepath.Join(tempDir, "resolv.conf")
	if err := ioutil.WriteFile(resolvConf, []byte("nameserver "+dns), 0644); err != nil {
		return common.Seq(cleanups), errors.Wrapf(err, "writing %s", resolvConf)
	}
	// TODO: use netlink
	cmds := [][]string{
		{"ip", "link", "set", tap, "up"},
		{"ip", "addr", "add", ip + "/" + strconv.Itoa(netmask), "dev", tap},
		{"ip", "route", "add", "default", "via", gateway, "dev", tap},
		{"mount", "--bind", resolvConf, "/etc/resolv.conf"},
	}
	if err := common.Execs(os.Stderr, os.Environ(), cmds); err != nil {
		return common.Seq(cleanups), errors.Wrapf(err, "executing %v", cmds)
	}
	return common.Seq(cleanups), nil
}

func setupNet(msg *common.Message) (func() error, error) {
	if msg.NetworkMode == common.HostNetwork {
		return common.Seq(nil), nil
	}
	return activateTap(msg.Tap, msg.IP, msg.Netmask, msg.Gateway, msg.DNS)
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
	cleanupNet, err := setupNet(msg)
	defer cleanupNet()
	if err != nil {
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
