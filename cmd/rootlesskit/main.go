package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/opencontainers/runc/libcontainer/user"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

const PipeFDEnvKey = "_ROOTLESSKIT_PIPEFD_UNDOCUMENTED"

var MagicPacket = []byte{0x42}

func main() {
	debug := false
	app := cli.NewApp()
	app.Name = "rootlesskit"
	app.Usage = "the gate to the rootless world"
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:        "debug",
			Usage:       "debug mode",
			Destination: &debug,
		},
	}
	app.Before = func(context *cli.Context) error {
		if debug {
			logrus.SetLevel(logrus.DebugLevel)
		}
		return nil
	}
	app.Action = func(clicontext *cli.Context) error {
		if clicontext.NArg() < 1 {
			return errors.New("no command specified")
		}
		if amIChild() {
			return child(clicontext)
		}
		return parent(clicontext)
	}
	if err := app.Run(os.Args); err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "error: %+v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
		// TODO: propagate the exit code from the real process
		os.Exit(1)
	}
}

func amIChild() bool {
	return os.Getenv(PipeFDEnvKey) != ""
}

func parent(clicontext *cli.Context) error {
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
	cmd.Env = append(os.Environ(), PipeFDEnvKey+"=3")
	if err := cmd.Start(); err != nil {
		return errors.Wrap(err, "failed to start the child")
	}
	if err := setupUIDGIDMap(cmd.Process.Pid); err != nil {
		return errors.Wrap(err, "failed to setup UID/GID map")
	}

	// wake up the child
	if _, err := pipeW.Write(MagicPacket); err != nil {
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

func childWaitForParentSync(pipeFDStr string) error {
	pipeFD, err := strconv.Atoi(pipeFDStr)
	if err != nil {
		return errors.Wrapf(err, "unexpected fd value: %s", pipeFDStr)
	}
	pipeR := os.NewFile(uintptr(pipeFD), "")
	buf := make([]byte, len(MagicPacket))
	if _, err := pipeR.Read(buf); err != nil {
		return errors.Wrapf(err, "failed to read fd %d", pipeFD)
	}
	if bytes.Compare(MagicPacket, buf) != 0 {
		return errors.Errorf("expected magic packet %v, got %v", MagicPacket, buf)
	}
	return pipeR.Close()
}

func child(clicontext *cli.Context) error {
	pipeFDStr := os.Getenv(PipeFDEnvKey)
	if pipeFDStr == "" {
		return errors.Errorf("%s is not set", PipeFDEnvKey)
	}
	os.Unsetenv(PipeFDEnvKey)
	if err := childWaitForParentSync(pipeFDStr); err != nil {
		return err
	}
	fullArgs := clicontext.Args()
	var args []string
	if len(fullArgs) > 1 {
		args = fullArgs[1:]
	}
	cmd := exec.Command(fullArgs[0], args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	return cmd.Run()
}
