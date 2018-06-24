package child

import (
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/pkg/errors"

	"github.com/AkihiroSuda/rootlesskit/pkg/common"
)

func mountResolvConf(tempDir, dns string) error {
	myResolvConf := filepath.Join(tempDir, "resolv.conf")
	if err := ioutil.WriteFile(myResolvConf, []byte("nameserver "+dns+"\n"), 0644); err != nil {
		return errors.Wrapf(err, "writing %s", myResolvConf)
	}
	hostResolvConf, err := filepath.EvalSymlinks("/etc/resolv.conf")
	if err != nil {
		return errors.Wrap(err, "evaluating /etc/resolv.conf on the initial namespace")
	}
	if filepath.Dir(hostResolvConf) == "/run/systemd/resolve" {
		return mountResolvConfWithSystemdHack(myResolvConf, hostResolvConf)
	}
	return mountResolvConfWithoutSystemdHack(myResolvConf, hostResolvConf)
}

func mountResolvConfWithoutSystemdHack(myResolvConf, hostResolvConf string) error {
	cmds := [][]string{
		{"mount", "--bind", myResolvConf, "/etc/resolv.conf"},
	}
	if err := common.Execs(os.Stderr, os.Environ(), cmds); err != nil {
		return errors.Wrapf(err, "executing %v", cmds)
	}
	return nil
}

// mountResolvConfWithSystemdHack mounts resolv.conf with systemd-specific hack.
//
// When /etc/resolv.conf is a symlink to ../run/systemd/resolve/stub-resolv.conf,
// our bind-mounted /etc/resolv.conf (in our namespaces) is unexpectedly unmounted
// when /run/systemd/resolve/stub-resolv.conf is recreated.
//
// So we mask /run/systemd/resolve using tmpfs.
// See https://github.com/AkihiroSuda/rootlesskit/issues/4
func mountResolvConfWithSystemdHack(myResolvConf, hostResolvConf string) error {
	cmds := [][]string{
		{"mount", "-t", "tmpfs", "none", filepath.Dir(hostResolvConf)},
		{"touch", hostResolvConf},
		{"mount", "--bind", myResolvConf, hostResolvConf},
	}
	if err := common.Execs(os.Stderr, os.Environ(), cmds); err != nil {
		return errors.Wrapf(err, "executing %v", cmds)
	}
	return nil
}
