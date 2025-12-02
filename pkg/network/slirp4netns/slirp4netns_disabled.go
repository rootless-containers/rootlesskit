//go:build no_slirp4netns
// +build no_slirp4netns

package slirp4netns

import (
	"context"
	"errors"
	"io"
	"net"

	"github.com/rootless-containers/rootlesskit/v3/pkg/api"
	"github.com/rootless-containers/rootlesskit/v3/pkg/messages"
	"github.com/rootless-containers/rootlesskit/v3/pkg/network"
)

// Features is defined to satisfy references from cmd when the slirp4netns
// network driver is disabled via the no_slirp4netns build tag.
// It mirrors the shape of the real Features struct.
type Features struct {
	// SupportsEnableIPv6 --enable-ipv6 (v0.2.0)
	SupportsEnableIPv6 bool
	// SupportsCIDR --cidr (v0.3.0)
	SupportsCIDR bool
	// SupportsDisableHostLoopback --disable-host-loopback (v0.3.0)
	SupportsDisableHostLoopback bool
	// SupportsAPISocket --api-socket (v0.3.0)
	SupportsAPISocket bool
	// SupportsEnableSandbox --enable-sandbox (v0.4.0)
	SupportsEnableSandbox bool
	// SupportsEnableSeccomp --enable-seccomp (v0.4.0)
	SupportsEnableSeccomp bool
	// KernelSupportsEnableSeccomp whether the kernel supports slirp4netns --enable-seccomp
	KernelSupportsEnableSeccomp bool
}

// DetectFeatures is a stub used when the slirp4netns network driver is
// disabled via the no_slirp4netns build tag. It always returns an error so
// callers can gracefully handle the lack of support at runtime.
func DetectFeatures(binary string) (*Features, error) {
	return nil, errors.New("slirp4netns network driver disabled by build tag no_slirp4netns")
}

// NewParentDriver returns a stub when built with the no_slirp4netns tag.
func NewParentDriver(logWriter io.Writer, binary string, mtu int, ipnet *net.IPNet, ifname string, disableHostLoopback bool, apiSocketPath string, enableSandbox bool, enableSeccomp bool, enableIPv6 bool) (network.ParentDriver, error) {
	return &disabledParent{}, errors.New("slirp4netns network driver disabled by build tag no_slirp4netns")
}

type disabledParent struct{}

func (d *disabledParent) Info(ctx context.Context) (*api.NetworkDriverInfo, error) {
	return nil, errors.New("slirp4netns network driver disabled by build tag no_slirp4netns")
}

func (d *disabledParent) MTU() int { return 0 }

func (d *disabledParent) ConfigureNetwork(childPID int, stateDir string, detachedNetNSPath string) (*messages.ParentInitNetworkDriverCompleted, func() error, error) {
	return nil, func() error { return nil }, errors.New("slirp4netns network driver disabled by build tag no_slirp4netns")
}

// NewChildDriver returns a stub when built with the no_slirp4netns tag.
func NewChildDriver() network.ChildDriver { return &disabledChild{} }

type disabledChild struct{}

func (d *disabledChild) ChildDriverInfo() (*network.ChildDriverInfo, error) {
	return &network.ChildDriverInfo{ConfiguresInterface: false}, nil
}

func (d *disabledChild) ConfigureNetworkChild(netmsg *messages.ParentInitNetworkDriverCompleted, detachedNetNSPath string) (string, error) {
	return "", errors.New("slirp4netns network driver disabled by build tag no_slirp4netns")
}

// Available indicates whether this driver is compiled in (used for generating help text)
const Available = false
