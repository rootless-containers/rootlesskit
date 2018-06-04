package util

import (
	"os/exec"
	"syscall"

	"github.com/pkg/errors"
)

func GetExecExitStatus(err error) (int, bool) {
	err = errors.Cause(err)
	if err == nil {
		return 0, false
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		return 0, false
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		return 0, false
	}
	return status.ExitStatus(), true
}
