package parentutils

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/rootless-containers/rootlesskit/v2/pkg/common"
)

func PrepareTap(childPID int, childNetNsPath string, tap string) error {
	sameUserNSAsCurrent, err := SameUserNSAsCurrent(childPID)
	if err != nil {
		return err
	}
	userns := !sameUserNSAsCurrent
	cmds := [][]string{
		NSEnter(childPID, childNetNsPath, userns, []string{"ip", "tuntap", "add", "name", tap, "mode", "tap"}),
		NSEnter(childPID, childNetNsPath, userns, []string{"ip", "link", "set", tap, "up"}),
	}
	if err := common.Execs(os.Stderr, os.Environ(), cmds); err != nil {
		return fmt.Errorf("executing %v: %w", cmds, err)
	}
	return nil
}

func NSEnter(childPID int, childNetNsPath string, userns bool, cmd []string) []string {
	fullCmd := []string{"nsenter", "-t", strconv.Itoa(childPID)}
	if childNetNsPath != "" {
		fullCmd = append(fullCmd, "-n"+childNetNsPath)
	} else {
		fullCmd = append(fullCmd, "-n")
	}
	fullCmd = append(fullCmd, "-m")
	if userns {
		fullCmd = append(fullCmd, []string{"-U", "--preserve-credentials"}...)
	}
	fullCmd = append(fullCmd, cmd...)
	return fullCmd
}

func SameNS(pid [2]int, nsName string) (bool, error) {
	var links [2]string
	for i := 0; i < 2; i++ {
		p := filepath.Join("/proc", strconv.Itoa(pid[i]), "ns", filepath.Clean(nsName))
		var err error
		links[i], err = os.Readlink(p)
		if err != nil {
			return false, err
		}
	}
	return links[0] == links[1], nil
}

func SameUserNSAsCurrent(pid int) (bool, error) {
	return SameNS([2]int{os.Getpid(), pid}, "user")
}
