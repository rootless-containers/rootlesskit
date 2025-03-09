package none

import (
	"context"
	"os"

	"github.com/rootless-containers/rootlesskit/v2/pkg/api"
	"github.com/rootless-containers/rootlesskit/v2/pkg/common"
	"github.com/rootless-containers/rootlesskit/v2/pkg/messages"
	"github.com/rootless-containers/rootlesskit/v2/pkg/network"
	"github.com/rootless-containers/rootlesskit/v2/pkg/network/parentutils"
)

func NewParentDriver() (network.ParentDriver, error) {
	return &parentDriver{}, nil
}

type parentDriver struct {
}

const DriverName = "none"

func (d *parentDriver) MTU() int {
	return 0
}

func (d *parentDriver) Info(ctx context.Context) (*api.NetworkDriverInfo, error) {
	return &api.NetworkDriverInfo{
		Driver: DriverName,
	}, nil
}

func (d *parentDriver) ConfigureNetwork(childPID int, stateDir, detachedNetNSPath string) (*messages.ParentInitNetworkDriverCompleted, func() error, error) {
	var cleanups []func() error

	sameUserNSAsCurrent, err := parentutils.SameUserNSAsCurrent(childPID)
	if err != nil {
		return nil, nil, err
	}
	userns := !sameUserNSAsCurrent

	cmds := [][]string{
		parentutils.NSEnter(childPID, detachedNetNSPath, userns, []string{"ip", "address", "add", "127.0.0.1/8", "dev", "lo"}),
		parentutils.NSEnter(childPID, detachedNetNSPath, userns, []string{"ip", "link", "set", "lo", "up"}),
	}
	if err := common.Execs(os.Stderr, os.Environ(), cmds); err != nil {
		return nil, nil, err
	}

	netmsg := messages.ParentInitNetworkDriverCompleted{}
	return &netmsg, common.Seq(cleanups), nil
}
