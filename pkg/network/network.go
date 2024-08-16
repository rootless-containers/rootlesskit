package network

import (
	"context"

	"github.com/rootless-containers/rootlesskit/v2/pkg/api"
	"github.com/rootless-containers/rootlesskit/v2/pkg/messages"
)

// ParentDriver is called from the parent namespace
type ParentDriver interface {
	Info(ctx context.Context) (*api.NetworkDriverInfo, error)
	// MTU returns MTU
	MTU() int
	// ConfigureNetwork sets up Slirp, updates msg, and returns destructor function.
	// detachedNetNSPath is set only for the detach-netns mode.
	ConfigureNetwork(childPID int, stateDir, detachedNetNSPath string) (netmsg *messages.ParentInitNetworkDriverCompleted, cleanup func() error, err error)
}

type ChildDriverInfo struct {
	ConfiguresInterface bool // Driver configures own namespace interface
}

// ChildDriver is called from the child namespace
type ChildDriver interface {
	// ConfigureNetworkChild is executed in the child's namespaces, excluding detached-netns.
	//
	// netmsg MAY be modified.
	// devName is like "tap" or "eth0"
	ConfigureNetworkChild(netmsg *messages.ParentInitNetworkDriverCompleted, detachedNetNSPath string) (devName string, err error)

	ChildDriverInfo() (*ChildDriverInfo, error)
}
