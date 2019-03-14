package vpnkit

import (
	"context"
	"io"
	"net"
	"sync"

	"github.com/moby/vpnkit/go/pkg/vpnkit"
	"github.com/pkg/errors"
	"github.com/rootless-containers/rootlesskit/pkg/port"
	"github.com/rootless-containers/rootlesskit/pkg/port/portutil"
)

func NewParentDriver(logWriter io.Writer, apiSocketPath string) (port.ParentDriver, error) {
	if apiSocketPath == "" {
		return nil, errors.New("api socket path is not set")
	}
	d := driver{
		logWriter:     logWriter,
		apiSocketPath: apiSocketPath,
		ports:         make(map[int]*port.Status, 0),
		vports:        make(map[int]*vpnkit.Port, 0),
		nextID:        1,
	}
	return &d, nil
}

type driver struct {
	logWriter     io.Writer
	apiSocketPath string
	c             *vpnkit.Connection
	mu            sync.Mutex
	childIP       net.IP
	ports         map[int]*port.Status
	vports        map[int]*vpnkit.Port
	nextID        int
}

func (d *driver) OpaqueForChild() map[string]string {
	// NOP, as this driver does not have child-side logic.
	return nil
}

func (d *driver) RunParentDriver(initComplete chan struct{}, quit <-chan struct{}, cctx *port.ChildContext) error {
	if cctx == nil || cctx.IP == nil {
		return errors.New("got empty child IP")
	}
	d.childIP = cctx.IP
	var err error
	d.c, err = vpnkit.NewConnection(context.Background(), d.apiSocketPath)
	if err != nil {
		return err
	}

	initComplete <- struct{}{}
	<-quit
	return nil
}

func (d *driver) AddPort(ctx context.Context, spec port.Spec) (*port.Status, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	err := portutil.ValidatePortSpec(spec, d.ports)
	if err != nil {
		return nil, err
	}
	vp := &vpnkit.Port{
		Proto:   vpnkit.Protocol(spec.Proto),
		OutIP:   net.ParseIP(spec.ParentIP),
		OutPort: uint16(spec.ParentPort),
		InIP:    d.childIP,
		InPort:  uint16(spec.ChildPort),
	}
	if err = d.c.Expose(ctx, vp); err != nil {
		return nil, err
	}
	id := d.nextID
	st := port.Status{
		ID:   id,
		Spec: spec,
	}
	d.ports[id] = &st
	d.vports[id] = vp
	d.nextID++
	return &st, nil
}

func (d *driver) ListPorts(ctx context.Context) ([]port.Status, error) {
	var ports []port.Status
	d.mu.Lock()
	for _, p := range d.ports {
		ports = append(ports, *p)
	}
	d.mu.Unlock()
	return ports, nil
}

func (d *driver) RemovePort(ctx context.Context, id int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	vp, ok := d.vports[id]
	if !ok {
		return errors.Errorf("unknown port id: %d", id)
	}
	d.c.Unexpose(ctx, vp)
	delete(d.ports, id)
	delete(d.vports, id)
	return nil
}

func NewChildDriver() port.ChildDriver {
	return &childDriver{}
}

type childDriver struct {
}

func (d *childDriver) RunChildDriver(opaque map[string]string, quit <-chan struct{}) error {
	// NOP
	<-quit
	return nil
}
