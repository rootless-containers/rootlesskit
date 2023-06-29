package parentutils

import (
	"fmt"
	"os"
	"strconv"

	"github.com/rootless-containers/rootlesskit/v2/pkg/common"
)

func PrepareTap(childPID int, childNetNsPath string, tap string) error {
	cmds := [][]string{
		nsenter(childPID, childNetNsPath, []string{"ip", "tuntap", "add", "name", tap, "mode", "tap"}),
		nsenter(childPID, childNetNsPath, []string{"ip", "link", "set", tap, "up"}),
	}
	if err := common.Execs(os.Stderr, os.Environ(), cmds); err != nil {
		return fmt.Errorf("executing %v: %w", cmds, err)
	}
	return nil
}

func nsenter(childPID int, childNetNsPath string, cmd []string) []string {
	fullCmd := []string{"nsenter", "-t", strconv.Itoa(childPID)}
	if childNetNsPath != "" {
		fullCmd = append(fullCmd, "-n"+childNetNsPath)
	} else {
		fullCmd = append(fullCmd, "-n")
	}
	fullCmd = append(fullCmd, []string{"-m", "-U", "--preserve-credentials"}...)
	fullCmd = append(fullCmd, cmd...)
	return fullCmd
}
