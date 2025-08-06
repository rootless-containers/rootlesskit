package parent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/gofrs/flock"
	"github.com/gorilla/mux"
	"github.com/rootless-containers/rootlesskit/v2/pkg/api/router"
	"github.com/rootless-containers/rootlesskit/v2/pkg/messages"
	"github.com/rootless-containers/rootlesskit/v2/pkg/network"
	"github.com/rootless-containers/rootlesskit/v2/pkg/parent/cgrouputil"
	"github.com/rootless-containers/rootlesskit/v2/pkg/parent/dynidtools"
	"github.com/rootless-containers/rootlesskit/v2/pkg/parent/idtools"
	"github.com/rootless-containers/rootlesskit/v2/pkg/port"
	"github.com/rootless-containers/rootlesskit/v2/pkg/sigproxy"
	"github.com/rootless-containers/rootlesskit/v2/pkg/sigproxy/signal"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type Opt struct {
	PipeFDEnvKey             string               // needs to be set
	ChildUseActivationEnvKey string               // needs to be set
	StateDir                 string               // directory needs to be precreated
	StateDirEnvKey           string               // optional env key to propagate StateDir value
	NetworkDriver            network.ParentDriver // nil for HostNetwork
	PortDriver               port.ParentDriver    // nil for --port-driver=none
	PublishPorts             []port.Spec
	CreatePIDNS              bool
	CreateCgroupNS           bool
	CreateUTSNS              bool
	CreateIPCNS              bool
	DetachNetNS              bool
	ParentEUIDEnvKey         string // optional env key to propagate geteuid() value
	ParentEGIDEnvKey         string // optional env key to propagate getegid() value
	Propagation              string
	EvacuateCgroup2          string // e.g. "rootlesskit_evacuation"
	SubidSource              SubidSource
}

type SubidSource string

const (
	SubidSourceAuto    = SubidSource("auto")    // Try dynamic then fallback to static
	SubidSourceDynamic = SubidSource("dynamic") // /usr/bin/getsubids
	SubidSourceStatic  = SubidSource("static")  // /etc/{subuid,subgid}
)

// Documented state files. Undocumented ones are subject to change.
const (
	StateFileLock     = "lock"
	StateFileChildPID = "child_pid" // decimal pid number text
	StateFileAPISock  = "api.sock"  // REST API Socket
	StateFileNetNs    = "netns"     // rootlesskit network namespace
)

func checkPreflight(opt Opt) error {
	if opt.PipeFDEnvKey == "" {
		return errors.New("pipe FD env key is not set")
	}
	if opt.StateDir == "" {
		return errors.New("state dir is not set")
	}
	if !filepath.IsAbs(opt.StateDir) {
		return errors.New("state dir must be absolute")
	}
	if stat, err := os.Stat(opt.StateDir); err != nil || !stat.IsDir() {
		return fmt.Errorf("state dir is inaccessible: %w", err)
	}

	if os.Geteuid() == 0 {
		logrus.Warn("Running RootlessKit as the root user is unsupported.")
	}

	warnSysctl()

	// invalid propagation doesn't result in an error
	warnPropagation(opt.Propagation)
	return nil
}

// createCleanupLock uses LOCK_SH for preventing automatic cleanup of
// "/tmp/<Our State Dir>" caused by by systemd.
//
// This LOCK_SH lock is different from our lock file in the state dir.
// We could unify the lock file into LOCK_SH, but we are still keeping
// the lock file for a historical reason.
//
// See:
// - https://github.com/rootless-containers/rootlesskit/issues/185
// - https://github.com/rootless-containers/rootlesskit/pull/188
func createCleanupLock(sDir string) error {
	//lock state dir when using /tmp/ path
	stateDir, err := os.Open(sDir)
	if err != nil {
		return err
	}
	err = unix.Flock(int(stateDir.Fd()), unix.LOCK_SH)
	if err != nil {
		logrus.Warnf("Failed to lock the state dir %s", sDir)
	}
	return nil
}

// LockStateDir creates and locks "lock" file in the state dir.
func LockStateDir(stateDir string) (*flock.Flock, error) {
	lockPath := filepath.Join(stateDir, StateFileLock)
	lock := flock.New(lockPath)
	locked, err := lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("failed to lock %s: %w", lockPath, err)
	}
	if !locked {
		return nil, fmt.Errorf("failed to lock %s, another RootlessKit is running with the same state directory?", lockPath)
	}
	return lock, nil
}

func setupFilesAndEnv(readPipe *os.File, writePipe *os.File, opt Opt) ([]*os.File, []string) {
	// 0 1 and 2  are used for stdin. stdout, and stderr
	const listenFdsStart = 3
	listenPid, listenPidErr := strconv.Atoi(os.Getenv("LISTEN_PID"))
	listenFds, listenFdsErr := strconv.Atoi(os.Getenv("LISTEN_FDS"))
	useSystemdSocketFDs := listenPidErr == nil && listenFdsErr == nil && listenFds > 0
	if !useSystemdSocketFDs {
		listenFds = 0
	}
	extraFiles := make([]*os.File, listenFds+2)
	for i, fd := 0, listenFdsStart; i < listenFds; i, fd = i+1, fd+1 {
		name := "LISTEN_FD_" + strconv.Itoa(fd)
		extraFiles[i] = os.NewFile(uintptr(fd), name)
	}
	extraFiles[listenFds] = readPipe
	extraFiles[listenFds+1] = writePipe
	cmdEnv := os.Environ()
	cmdEnv = append(cmdEnv, opt.PipeFDEnvKey+"="+strconv.Itoa(listenFdsStart+listenFds)+","+strconv.Itoa(listenFdsStart+listenFds+1))
	cmdEnv = append(cmdEnv, opt.ChildUseActivationEnvKey+"="+strconv.FormatBool(listenPid == os.Getpid()))
	return extraFiles, cmdEnv
}

func Parent(opt Opt) error {
	if err := checkPreflight(opt); err != nil {
		return err
	}

	err := createCleanupLock(opt.StateDir)
	if err != nil {
		return err
	}

	lock, err := LockStateDir(opt.StateDir)
	if err != nil {
		return err
	}
	defer os.RemoveAll(opt.StateDir)
	defer lock.Unlock()

	pipeR, pipeW, err := os.Pipe() // parent-to-child
	if err != nil {
		return err
	}
	pipe2R, pipe2W, err := os.Pipe() // child-to-parent
	if err != nil {
		return err
	}
	cmd := exec.Command("/proc/self/exe", os.Args[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig:  syscall.SIGKILL,
		Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS,
	}

	if opt.NetworkDriver != nil {
		if !opt.DetachNetNS {
			cmd.SysProcAttr.Unshareflags |= syscall.CLONE_NEWNET
		}
	}

	if opt.CreatePIDNS {
		// cannot be Unshareflags (panics)
		cmd.SysProcAttr.Cloneflags |= syscall.CLONE_NEWPID
	}
	if opt.CreateCgroupNS {
		cmd.SysProcAttr.Unshareflags |= unix.CLONE_NEWCGROUP
	}
	if opt.CreateUTSNS {
		cmd.SysProcAttr.Unshareflags |= unix.CLONE_NEWUTS
	}
	if opt.CreateIPCNS {
		cmd.SysProcAttr.Unshareflags |= unix.CLONE_NEWIPC
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles, cmd.Env = setupFilesAndEnv(pipeR, pipe2W, opt)
	if opt.StateDirEnvKey != "" {
		cmd.Env = append(cmd.Env, opt.StateDirEnvKey+"="+opt.StateDir)
	}
	if opt.ParentEUIDEnvKey != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%d", opt.ParentEUIDEnvKey, os.Geteuid()))
	}
	if opt.ParentEGIDEnvKey != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%d", opt.ParentEGIDEnvKey, os.Getegid()))
	}
	if err := cmd.Start(); err != nil {
		warnOnChildStartFailure(err)
		return fmt.Errorf("failed to start the child: %w", err)
	}

	msgParentHello := &messages.Message{
		U: messages.U{
			ParentHello: &messages.ParentHello{},
		},
	}
	if err := messages.Send(pipeW, msgParentHello); err != nil {
		return err
	}
	if _, err := messages.WaitFor(pipe2R, messages.Name(messages.ChildHello{})); err != nil {
		return err
	}

	if err := setupUIDGIDMap(cmd.Process.Pid, opt.SubidSource); err != nil {
		return fmt.Errorf("failed to setup UID/GID map: %w", err)
	}
	msgParentInitIdmapCompleted := &messages.Message{
		U: messages.U{
			ParentInitIdmapCompleted: &messages.ParentInitIdmapCompleted{},
		},
	}
	if err := messages.Send(pipeW, msgParentInitIdmapCompleted); err != nil {
		return err
	}
	if _, err := messages.WaitFor(pipe2R, messages.Name(messages.ChildInitUserNSCompleted{})); err != nil {
		return err
	}

	sigc := sigproxy.ForwardAllSignals(context.TODO(), cmd.Process.Pid)
	defer signal.StopCatch(sigc)

	if opt.EvacuateCgroup2 != "" {
		if err := cgrouputil.EvacuateCgroup2(opt.EvacuateCgroup2); err != nil {
			return err
		}
	}

	// configure Network driver
	msgParentInitNetworkDriverCompleted := &messages.Message{
		U: messages.U{
			ParentInitNetworkDriverCompleted: &messages.ParentInitNetworkDriverCompleted{},
		},
	}

	if opt.NetworkDriver != nil {
		var netns string
		if opt.DetachNetNS {
			netns = filepath.Join("/proc", strconv.Itoa(cmd.Process.Pid), "root", filepath.Clean(opt.StateDir), "netns")
		}
		netMsg, cleanupNetwork, err := opt.NetworkDriver.ConfigureNetwork(cmd.Process.Pid, opt.StateDir, netns)
		if cleanupNetwork != nil {
			defer cleanupNetwork()
		}
		if err != nil {
			return fmt.Errorf("failed to setup network %+v: %w", opt.NetworkDriver, err)
		}
		msgParentInitNetworkDriverCompleted.U.ParentInitNetworkDriverCompleted = netMsg
	}
	if err := messages.Send(pipeW, msgParentInitNetworkDriverCompleted); err != nil {
		return err
	}

	// configure Port driver
	msgParentInitPortDriverCompleted := &messages.Message{
		U: messages.U{
			ParentInitPortDriverCompleted: &messages.ParentInitPortDriverCompleted{},
		},
	}
	portDriverInitComplete := make(chan struct{})
	portDriverQuit := make(chan struct{})
	portDriverErr := make(chan error)
	if opt.PortDriver != nil {
		msgParentInitPortDriverCompleted.U.ParentInitPortDriverCompleted.PortDriverOpaque = opt.PortDriver.OpaqueForChild()
		cctx := &port.ChildContext{
			IP:      net.ParseIP(msgParentInitNetworkDriverCompleted.U.ParentInitNetworkDriverCompleted.IP).To4(),
			Network: msgParentInitNetworkDriverCompleted.U.ParentInitNetworkDriverCompleted.Network,
		}
		go func() {
			portDriverErr <- opt.PortDriver.RunParentDriver(portDriverInitComplete,
				portDriverQuit, cctx)
		}()
	}
	if err := messages.Send(pipeW, msgParentInitPortDriverCompleted); err != nil {
		return err
	}

	// Close the parent-to-child pipe
	if err := pipeW.Close(); err != nil {
		return err
	}
	if opt.PortDriver != nil {
		// wait for port driver to be ready
		select {
		case <-portDriverInitComplete:
		case err = <-portDriverErr:
			return err
		}
		// publish ports
		for _, p := range opt.PublishPorts {
			st, err := opt.PortDriver.AddPort(context.TODO(), p)
			if err != nil {
				return fmt.Errorf("failed to expose port %v: %w", p, err)
			}
			logrus.Debugf("published port %v", st)
		}
	}

	// after child is fully configured, write PID to child_pid file
	childPIDPath := filepath.Join(opt.StateDir, StateFileChildPID)
	if err := os.WriteFile(childPIDPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0444); err != nil {
		return fmt.Errorf("failed to write the child PID %d to %s: %w", cmd.Process.Pid, childPIDPath, err)
	}
	// listens the API
	apiSockPath := filepath.Join(opt.StateDir, StateFileAPISock)
	apiCloser, err := listenServeAPI(apiSockPath, &router.Backend{
		StateDir:      opt.StateDir,
		ChildPID:      cmd.Process.Pid,
		NetworkDriver: opt.NetworkDriver,
		PortDriver:    opt.PortDriver,
	})
	if err != nil {
		return err
	}
	// block until the child exits
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("child exited: %w", err)
	}
	// close the API socket
	if err := apiCloser.Close(); err != nil {
		return fmt.Errorf("failed to close %s: %w", apiSockPath, err)
	}
	// shut down port driver
	if opt.PortDriver != nil {
		portDriverQuit <- struct{}{}
		err = <-portDriverErr
	}
	return err
}

func getSubIDRanges(u *user.User, subidSource SubidSource) ([]idtools.SubIDRange, []idtools.SubIDRange, error) {
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return nil, nil, err
	}
	switch subidSource {
	case SubidSourceStatic:
		logrus.Debugf("subid-source: using the static source")
		return idtools.GetSubIDRanges(uid, u.Username)
	case SubidSourceDynamic:
		logrus.Debugf("subid-source: using the dynamic source")
		return dynidtools.GetSubIDRanges(uid, u.Username)
	case "", SubidSourceAuto:
		subuidRanges, subgidRanges, err := getSubIDRanges(u, SubidSourceDynamic)
		if err == nil && len(subuidRanges) > 0 && len(subgidRanges) > 0 {
			return subuidRanges, subgidRanges, nil
		}
		logrus.WithError(err).Debugf("failed to use subid source %q, falling back to %q", SubidSourceDynamic, SubidSourceStatic)
		return getSubIDRanges(u, SubidSourceStatic)
	default:
		return nil, nil, fmt.Errorf("unknown subid source %q", subidSource)
	}
}

func newugidmapArgs(subidSource SubidSource) ([]string, []string, error) {
	u, err := user.Current()
	if err != nil {
		return nil, nil, err
	}
	subuidRanges, subgidRanges, err := getSubIDRanges(u, subidSource)
	if err != nil {
		return nil, nil, err
	}
	logrus.Debugf("subuid ranges=%v", subuidRanges)
	logrus.Debugf("subgid ranges=%v", subgidRanges)
	return newugidmapArgsFromSubIDRanges(u, subuidRanges, subgidRanges)
}

func newugidmapArgsFromSubIDRanges(u *user.User, subuidRanges, subgidRanges []idtools.SubIDRange) ([]string, []string, error) {
	uidMap := []string{
		"0",
		u.Uid,
		"1",
	}
	gidMap := []string{
		"0",
		u.Gid,
		"1",
	}

	uidMapLast := 1
	for _, f := range subuidRanges {
		uidMap = append(uidMap, []string{
			strconv.Itoa(uidMapLast),
			strconv.Itoa(f.Start),
			strconv.Itoa(f.Length),
		}...)
		uidMapLast += f.Length
	}
	gidMapLast := 1
	for _, f := range subgidRanges {
		gidMap = append(gidMap, []string{
			strconv.Itoa(gidMapLast),
			strconv.Itoa(f.Start),
			strconv.Itoa(f.Length),
		}...)
		gidMapLast += f.Length
	}

	return uidMap, gidMap, nil
}

func setupUIDGIDMap(pid int, subidSource SubidSource) error {
	uArgs, gArgs, err := newugidmapArgs(subidSource)
	if err != nil {
		return fmt.Errorf("failed to compute uid/gid map: %w", err)
	}
	pidS := strconv.Itoa(pid)
	cmd := exec.Command("newuidmap", append([]string{pidS}, uArgs...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("newuidmap %s %v failed: %s: %w", pidS, uArgs, string(out), err)
	}
	cmd = exec.Command("newgidmap", append([]string{pidS}, gArgs...)...)
	out, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("newgidmap %s %v failed: %s: %w", pidS, gArgs, string(out), err)
	}
	return nil
}

// apiCloser is implemented by *http.Server
type apiCloser interface {
	Close() error
	Shutdown(context.Context) error
}

func listenServeAPI(socketPath string, backend *router.Backend) (apiCloser, error) {
	r := mux.NewRouter()
	router.AddRoutes(r, backend)
	srv := &http.Server{Handler: r}
	err := os.RemoveAll(socketPath)
	if err != nil {
		return nil, err
	}
	l, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	go srv.Serve(l)
	return srv, nil
}

// InitStateDir removes everything in the state dir except the lock file.
// This is needed because when the previous execution crashed, the state dir may not be removed successfully.
//
// InitStateDir must be called before calling parent functions.
func InitStateDir(stateDir string) error {
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return err
	}
	lk, err := LockStateDir(stateDir)
	if err != nil {
		return err
	}
	defer lk.Unlock()
	stateDirStuffs, err := os.ReadDir(stateDir)
	if err != nil {
		return err
	}
	for _, f := range stateDirStuffs {
		if f.Name() == StateFileLock {
			continue
		}
		p := filepath.Join(stateDir, f.Name())
		if err := os.RemoveAll(p); err != nil {
			return fmt.Errorf("failed to remove %s: %w", p, err)
		}
	}
	return nil
}
