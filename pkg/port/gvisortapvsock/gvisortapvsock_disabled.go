//go:build no_gvisortapvsock
// +build no_gvisortapvsock

package gvisortapvsock

import (
	"context"
	"errors"
	"io"

	"github.com/rootless-containers/rootlesskit/v3/pkg/api"
	"github.com/rootless-containers/rootlesskit/v3/pkg/port"
)

// NewParentDriver returns a stub when built with the no_gvisortapvsock tag.
func NewParentDriver(logWriter io.Writer, stateDir string) (port.ParentDriver, error) {
	return &disabledParent{}, errors.New("gvisor-tap-vsock port driver disabled by build tag no_gvisortapvsock")
}

type disabledParent struct{}

func (d *disabledParent) Info(ctx context.Context) (*api.PortDriverInfo, error) {
	return nil, errors.New("gvisor-tap-vsock port driver disabled by build tag no_gvisortapvsock")
}

func (d *disabledParent) OpaqueForChild() map[string]string { return map[string]string{} }

func (d *disabledParent) RunParentDriver(initComplete chan struct{}, quit <-chan struct{}, cctx *port.ChildContext) error {
	return errors.New("gvisor-tap-vsock port driver disabled by build tag no_gvisortapvsock")
}

func (d *disabledParent) AddPort(ctx context.Context, spec port.Spec) (*port.Status, error) {
	return nil, errors.New("gvisor-tap-vsock port driver disabled by build tag no_gvisortapvsock")
}

func (d *disabledParent) ListPorts(ctx context.Context) ([]port.Status, error) {
	return nil, errors.New("gvisor-tap-vsock port driver disabled by build tag no_gvisortapvsock")
}

func (d *disabledParent) RemovePort(ctx context.Context, id int) error {
	return errors.New("gvisor-tap-vsock port driver disabled by build tag no_gvisortapvsock")
}

// NewChildDriver returns a stub when built with the no_gvisortapvsock tag.
func NewChildDriver() port.ChildDriver { return &disabledChild{} }

type disabledChild struct{}

func (d *disabledChild) RunChildDriver(opaque map[string]string, quit <-chan struct{}, detachedNetNSPath string) error {
	return errors.New("gvisor-tap-vsock port driver disabled by build tag no_gvisortapvsock")
}

// Available indicates whether this port driver is compiled in (used for generating help text)
const Available = false
