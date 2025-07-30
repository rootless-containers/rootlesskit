//go:build no_vpnkit
// +build no_vpnkit

package vpnkit

import (
	"context"
	"errors"

	"github.com/rootless-containers/rootlesskit/v3/pkg/api"
	"github.com/rootless-containers/rootlesskit/v3/pkg/messages"
	"github.com/rootless-containers/rootlesskit/v3/pkg/network"
)

// NewParentDriver returns a stub when built with the no_vpnkit tag.
func NewParentDriver(binary string, mtu int, ifname string, disableHostLoopback bool) network.ParentDriver {
	return &disabledParent{}
}

type disabledParent struct{}

func (d *disabledParent) Info(ctx context.Context) (*api.NetworkDriverInfo, error) {
	return nil, errors.New("vpnkit network driver disabled by build tag no_vpnkit")
}

func (d *disabledParent) MTU() int { return 0 }

func (d *disabledParent) ConfigureNetwork(childPID int, stateDir, detachedNetNSPath string) (*messages.ParentInitNetworkDriverCompleted, func() error, error) {
	return nil, func() error { return nil }, errors.New("vpnkit network driver disabled by build tag no_vpnkit")
}

// NewChildDriver returns a stub when built with the no_vpnkit tag.
func NewChildDriver() network.ChildDriver { return &disabledChild{} }

type disabledChild struct{}

func (d *disabledChild) ChildDriverInfo() (*network.ChildDriverInfo, error) {
	return &network.ChildDriverInfo{ConfiguresInterface: false}, nil
}

func (d *disabledChild) ConfigureNetworkChild(netmsg *messages.ParentInitNetworkDriverCompleted, detachedNetNSPath string) (string, error) {
	return "", errors.New("vpnkit network driver disabled by build tag no_vpnkit")
}

// Available indicates whether this driver is compiled in (used for generating help text)
const Available = false
