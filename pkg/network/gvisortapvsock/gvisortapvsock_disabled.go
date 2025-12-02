//go:build no_gvisortapvsock
// +build no_gvisortapvsock

package gvisortapvsock

import (
	"context"
	"errors"
	"io"
	"net"

	"github.com/rootless-containers/rootlesskit/v3/pkg/api"
	"github.com/rootless-containers/rootlesskit/v3/pkg/messages"
	"github.com/rootless-containers/rootlesskit/v3/pkg/network"
)

// NewParentDriver returns a stub when built with the no_gvisortapvsock tag.
func NewParentDriver(logWriter io.Writer, mtu int, ipnet *net.IPNet, ifname string, disableHostLoopback bool, enableIPv6 bool) (network.ParentDriver, error) {
	return &disabledParent{}, errors.New("gvisor-tap-vsock network driver disabled by build tag no_gvisortapvsock")
}

type disabledParent struct{}

func (d *disabledParent) Info(ctx context.Context) (*api.NetworkDriverInfo, error) {
	return nil, errors.New("gvisor-tap-vsock network driver disabled by build tag no_gvisortapvsock")
}

func (d *disabledParent) MTU() int { return 0 }

func (d *disabledParent) ConfigureNetwork(childPID int, stateDir string, detachedNetNSPath string) (*messages.ParentInitNetworkDriverCompleted, func() error, error) {
	return nil, func() error { return nil }, errors.New("gvisor-tap-vsock network driver disabled by build tag no_gvisortapvsock")
}

// NewChildDriver returns a stub when built with the no_gvisortapvsock tag.
func NewChildDriver() network.ChildDriver { return &disabledChild{} }

type disabledChild struct{}

func (d *disabledChild) ChildDriverInfo() (*network.ChildDriverInfo, error) {
	return &network.ChildDriverInfo{ConfiguresInterface: false}, nil
}

func (d *disabledChild) ConfigureNetworkChild(netmsg *messages.ParentInitNetworkDriverCompleted, detachedNetNSPath string) (string, error) {
	return "", errors.New("gvisor-tap-vsock network driver disabled by build tag no_gvisortapvsock")
}

// Available indicates whether this driver is compiled in (used for generating help text)
const Available = false
