package parent

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/opencontainers/runc/libcontainer/user"
	"github.com/pkg/errors"
)

type Opt struct {
}

func Parent(pipeFDEnvKey string, magicPacket []byte, opt *Opt) error {
	if opt == nil {
		opt = &Opt{}
	}
	pipeR, pipeW, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd := exec.Command("/proc/self/exe", os.Args[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:   syscall.CLONE_NEWUSER,
		Unshareflags: syscall.CLONE_NEWNS,
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{pipeR}
	cmd.Env = append(os.Environ(), pipeFDEnvKey+"=3")
	if err := cmd.Start(); err != nil {
		return errors.Wrap(err, "failed to start the child")
	}
	if err := setupUIDGIDMap(cmd.Process.Pid); err != nil {
		return errors.Wrap(err, "failed to setup UID/GID map")
	}

	// wake up the child
	if _, err := pipeW.Write(magicPacket); err != nil {
		return err
	}
	if err := pipeW.Close(); err != nil {
		return err
	}
	if err := cmd.Wait(); err != nil {
		return errors.Wrap(err, "children exited")
	}
	return nil
}

func newuidmapArgs() ([]string, error) {
	u, err := user.CurrentUser()
	if err != nil {
		return nil, err
	}
	res := []string{
		"0",
		strconv.Itoa(u.Uid),
		"1",
	}
	subs, err := user.CurrentUserSubUIDs()
	if err != nil {
		return nil, err
	}
	// TODO: continue with non-subuid on ENOENT maybe
	last := 1
	for _, sub := range subs {
		res = append(res, []string{
			strconv.Itoa(last),
			strconv.Itoa(sub.SubID),
			strconv.Itoa(sub.Count),
		}...)
		last += sub.Count
	}
	return res, nil
}

func newgidmapArgs() ([]string, error) {
	g, err := user.CurrentGroup()
	if err != nil {
		return nil, err
	}
	res := []string{
		"0",
		strconv.Itoa(g.Gid),
		"1",
	}
	subs, err := user.CurrentGroupSubGIDs()
	if err != nil {
		return nil, err
	}
	// TODO: continue with non-subgid on ENOENT maybe
	last := 1
	for _, sub := range subs {
		res = append(res, []string{
			strconv.Itoa(last),
			strconv.Itoa(sub.SubID),
			strconv.Itoa(sub.Count),
		}...)
		last += sub.Count
	}
	return res, nil
}

func setupUIDGIDMap(pid int) error {
	uArgs, err := newuidmapArgs()
	if err != nil {
		return errors.Wrap(err, "failed to compute uid map")
	}
	gArgs, err := newgidmapArgs()
	if err != nil {
		return errors.Wrap(err, "failed to compute gid map")
	}
	pidS := strconv.Itoa(pid)
	cmd := exec.Command("newuidmap", append([]string{pidS}, uArgs...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return errors.Wrapf(err, "newuidmap %s %v failed: %s", pidS, uArgs, string(out))
	}
	cmd = exec.Command("newgidmap", append([]string{pidS}, gArgs...)...)
	out, err = cmd.CombinedOutput()
	if err != nil {
		return errors.Wrapf(err, "newgidmap %s %v failed: %s", pidS, gArgs, string(out))
	}
	return nil
}
