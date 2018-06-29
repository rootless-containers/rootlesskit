package parent

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/opencontainers/runc/libcontainer/user"
	"github.com/pkg/errors"

	"github.com/AkihiroSuda/rootlesskit/pkg/common"
)

type Opt struct {
	common.NetworkMode
	VPNKit VPNKitOpt
	common.CopyUpMode
	CopyUpDirs []string
}

type VPNKitOpt struct {
	Binary string
}

func Parent(pipeFDEnvKey string, opt *Opt) error {
	if opt == nil {
		opt = &Opt{}
	}
	pipeR, pipeW, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd := exec.Command("/proc/self/exe", os.Args[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig:    syscall.SIGKILL,
		Cloneflags:   syscall.CLONE_NEWUSER,
		Unshareflags: syscall.CLONE_NEWNS,
	}
	if opt.NetworkMode != common.HostNetwork {
		cmd.SysProcAttr.Unshareflags |= syscall.CLONE_NEWNET
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
	msg := common.Message{
		NetworkMode: opt.NetworkMode,
		CopyUpMode:  opt.CopyUpMode,
		CopyUpDirs:  opt.CopyUpDirs,
	}
	switch opt.NetworkMode {
	case common.VDEPlugSlirp:
		cleanupVDEPlugSlirp, err := setupVDEPlugSlirp(cmd.Process.Pid, &msg)
		defer cleanupVDEPlugSlirp()
		if err != nil {
			return errors.Wrap(err, "failed to setup vdeplug_slirp")
		}
	case common.VPNKit:
		cleanupVPNKit, err := setupVPNKit(cmd.Process.Pid, &msg, opt.VPNKit)
		defer cleanupVPNKit()
		if err != nil {
			return errors.Wrap(err, "failed to setup vpnkit")
		}
	}

	// wake up the child
	if err := writeMessage(pipeW, &msg); err != nil {
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

func writeMessage(w io.Writer, msg *common.Message) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	h := make([]byte, 4)
	binary.LittleEndian.PutUint32(h, uint32(len(b)))
	_, err = w.Write(append(h, b...))
	return err
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
			strconv.Itoa(int(sub.SubID)),
			strconv.Itoa(int(sub.Count)),
		}...)
		last += int(sub.Count)
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
			strconv.Itoa(int(sub.SubID)),
			strconv.Itoa(int(sub.Count)),
		}...)
		last += int(sub.Count)
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
