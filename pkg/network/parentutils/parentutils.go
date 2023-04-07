package parentutils

import (
	"fmt"
	"os"
	"path"
	"strconv"

	"github.com/rootless-containers/rootlesskit/pkg/common"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

func PrepareTap(childPID int, childNsPath string, tap string) error {
	cmds := [][]string{
		nsenter(childPID, childNsPath, []string{"ip", "tuntap", "add", "name", tap, "mode", "tap"}),
		nsenter(childPID, childNsPath, []string{"ip", "link", "set", tap, "up"}),
	}
	if err := common.Execs(os.Stderr, os.Environ(), cmds); err != nil {
		return fmt.Errorf("executing %v: %w", cmds, err)
	}
	return nil
}

func nsenter(childPID int, childNsPath string, cmd []string) []string {
	var fullCmd []string
	if childNsPath != "" {
		fullCmd = append([]string{"nsenter", "--net=/tmp/test/netns", "--preserve-credentials"}, cmd...)
	} else {
		fullCmd = append([]string{"nsenter", "-t", strconv.Itoa(childPID), "-n", "-m", "-U", "--preserve-credentials"}, cmd...)
	}
	return fullCmd
}

// NewNamedNetNs creates a new named network namespace in the childPid user namespace
// and mount it to bindMountPath. Current network namespace do not change
func NewNamedNetNs(name, bindMountPath string) error {
	if _, err := os.Stat(bindMountPath); os.IsNotExist(err) {
		err = os.MkdirAll(bindMountPath, 0755)
		if err != nil {
			return err
		}
	}
	origns, err := netns.Get()
	if err != nil {
		return err
	}

	newNs, err := netns.New()
	if err != nil {
		return err
	}
	namedPath := path.Join(bindMountPath, name)

	f, err := os.OpenFile(namedPath, os.O_CREATE|os.O_EXCL, 0444)
	if err != nil {
		if perr, ok := err.(*os.PathError); !ok && perr.Err.Error() != "file exists" {
			newNs.Close()
			return err
		}
	}
	f.Close()

	nsPath := fmt.Sprintf("/proc/%d/task/%d/ns/net", os.Getpid(), unix.Gettid())
	err = unix.Mount(nsPath, namedPath, "bind", unix.MS_BIND, "")
	if err != nil {
		newNs.Close()
		return err
	}
	// Switch back to the original namespace
	if err := netns.Set(origns); err != nil {
		return err
	}
	return nil
}
