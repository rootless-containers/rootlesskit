package activation

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

type Opt struct {
	RunActivationHelperEnvKey string   // needs to be set
	TargetCmd                 []string // needs to be set
}

func ActivationHelper(opt Opt) error {
	pid := os.Getpid()
	os.Unsetenv(opt.RunActivationHelperEnvKey)
	os.Setenv("LISTEN_PID", strconv.Itoa(pid))
	fixListenPidfdID()
	argsv := opt.TargetCmd
	execPath, err := exec.LookPath(argsv[0])
	if err != nil {
		return err
	}
	if err = syscall.Exec(execPath, argsv, os.Environ()); err != nil {
		return err
	}
	panic("should not reach here")
}

// fixListenPidfdID updates $LISTEN_PIDFDID to the pidfd inode ID of the current
// process. $LISTEN_PIDFDID is set by systemd v258 and later, in addition to
// $LISTEN_PID, so as to protect the file descriptor passing against PID reuse.
// sd_listen_fds(3) ignores the file descriptors when $LISTEN_PIDFDID does not
// correspond to the calling process, so it has to be rewritten together with
// $LISTEN_PID.
func fixListenPidfdID() {
	const key = "LISTEN_PIDFDID"
	if _, ok := os.LookupEnv(key); !ok {
		return
	}
	id, err := selfPidfdID()
	if err != nil {
		// Unique pidfd inode IDs need Linux 6.9 or later.
		// Just unset the variable so that the target command falls back to
		// checking $LISTEN_PID only.
		os.Unsetenv(key)
		return
	}
	os.Setenv(key, strconv.FormatUint(id, 10))
}

func selfPidfdID() (uint64, error) {
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		return 0, err
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return 0, err
	}
	return st.Ino, nil
}
