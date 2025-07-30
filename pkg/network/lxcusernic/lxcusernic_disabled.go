//go:build no_lxcusernic
// +build no_lxcusernic

package lxcusernic

import (
	"context"
	"errors"

	"github.com/rootless-containers/rootlesskit/v3/pkg/api"
	"github.com/rootless-containers/rootlesskit/v3/pkg/messages"
	"github.com/rootless-containers/rootlesskit/v3/pkg/network"
)

// NewParentDriver returns a stub when built with the no_lxcusernic tag.
func NewParentDriver(binary string, mtu int, bridge string, ifname string) (network.ParentDriver, error) {
	return &disabledParent{}, errors.New("lxc-user-nic network driver disabled by build tag no_lxcusernic")
}

type disabledParent struct{}

func (d *disabledParent) Info(ctx context.Context) (*api.NetworkDriverInfo, error) {
	return nil, errors.New("lxc-user-nic network driver disabled by build tag no_lxcusernic")
}

func (d *disabledParent) MTU() int { return 0 }

func (d *disabledParent) ConfigureNetwork(childPID int, stateDir string, detachedNetNSPath string) (*messages.ParentInitNetworkDriverCompleted, func() error, error) {
	return nil, func() error { return nil }, errors.New("lxc-user-nic network driver disabled by build tag no_lxcusernic")
}

// NewChildDriver returns a stub when built with the no_lxcusernic tag.
func NewChildDriver() network.ChildDriver { return &disabledChild{} }

type disabledChild struct{}

func (d *disabledChild) ChildDriverInfo() (*network.ChildDriverInfo, error) {
	return &network.ChildDriverInfo{ConfiguresInterface: false}, nil
}

func (d *disabledChild) ConfigureNetworkChild(netmsg *messages.ParentInitNetworkDriverCompleted, detachedNetNSPath string) (string, error) {
	return "", errors.New("lxc-user-nic network driver disabled by build tag no_lxcusernic")
}

// Available indicates whether this driver is compiled in (used for generating help text)
const Available = false
