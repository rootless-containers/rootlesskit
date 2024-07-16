package activation

import (
  "os"
  "os/exec"
  "syscall"
  "strconv"
)

type Opt struct {
  RunActivationHelperEnvKey  string   // needs to be set
  TargetCmd                  []string // needs to be set
}

func ActivationHelper(opt Opt) error {
  pid := os.Getpid()
  os.Unsetenv(opt.RunActivationHelperEnvKey)
  os.Setenv("LISTEN_PID", strconv.Itoa(pid))
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
