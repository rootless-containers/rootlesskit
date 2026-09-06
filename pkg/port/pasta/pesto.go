package pasta

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"sync"

	"github.com/rootless-containers/rootlesskit/v3/pkg/api"
	"github.com/rootless-containers/rootlesskit/v3/pkg/port"
	"github.com/rootless-containers/rootlesskit/v3/pkg/port/portutil"
	"github.com/sirupsen/logrus"
)

func NewParentDriver(logWriter io.Writer, binary, socketPath string, enableIPv6 bool) (port.ParentDriver, error) {
	if socketPath == "" {
		return nil, errors.New("configuration socket path is not set")
	}

	cmd := exec.Command(binary, "--version")
	b, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf(`command "%s --version" failed, make sure pesto is installed: %q: %w`,
			binary, string(b), err)
	}

	d := driver{
		logWriter:     logWriter,
		ports:         make(map[int]*port.Status),
		apiSocketPath: socketPath,
		binary:        binary,
		nextID:        1,
		enableIPv6:    enableIPv6,
	}

	return &d, nil
}

type driver struct {
	logWriter     io.Writer
	apiSocketPath string
	mu            sync.Mutex
	childIP       string // can be empty
	ports         map[int]*port.Status
	binary        string
	nextID        int
	enableIPv6    bool
}

func (d *driver) Info(ctx context.Context) (*api.PortDriverInfo, error) {
	protos := []string{"tcp", "tcp4", "udp", "udp4"}
	if d.enableIPv6 {
		protos = append(protos, "tcp6", "udp6")
	}
	info := &api.PortDriverInfo{
		Driver:                  "pesto",
		Protos:                  protos,
		DisallowLoopbackChildIP: true,
	}
	return info, nil
}

func (d *driver) OpaqueForChild() map[string]string {
	// NOP, as this driver does not have child-side logic.
	return nil
}

func (d *driver) RunParentDriver(initComplete chan struct{}, quit <-chan struct{}, cctx *port.ChildContext) error {
	if cctx != nil && cctx.IP != nil {
		if cctx.IP.To4() != nil {
			d.childIP = cctx.IP.To4().String()
		}
	}
	initComplete <- struct{}{}
	<-quit
	return nil
}

func (d *driver) createArgs(method string, spec port.Spec) ([]string, error) {
	opts := []string{method}
	o := ""

	switch spec.Proto {
	case "tcp", "tcp4", "tcp6":
		o += "--tcp-ports="
	case "udp", "udp4", "udp6":
		o += "--udp-ports="
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", spec.Proto)
	}
	parentIP := spec.ParentIP
	if parentIP == "" {
		switch spec.Proto {
		case "tcp", "tcp4", "udp", "udp4":
			parentIP = "0.0.0.0"
		case "tcp6", "udp6":
			parentIP = "::"
		}
	}
	p := net.ParseIP(parentIP)
	if p == nil {
		return nil, fmt.Errorf("invalid IP: %q", parentIP)
	}
	if !d.enableIPv6 && p.To4() == nil {
		return nil, fmt.Errorf("IPv6 port forwarding (proto %q, parent IP %q) requires --ipv6", spec.Proto, parentIP)
	}
	switch spec.Proto {
	case "tcp4", "udp4":
		if p.To4() == nil {
			return nil, fmt.Errorf("unsupported IP: %s", parentIP)
		}
	case "tcp6", "udp6":
		if p.To4() != nil {
			return nil, fmt.Errorf("unsupported IP: %s", parentIP)
		}
	}
	o += fmt.Sprintf("%s/%d:%d", p.String(), spec.ParentPort, spec.ChildPort)
	opts = append(opts, o)

	return append(opts, d.apiSocketPath), nil
}

func (d *driver) AddPort(ctx context.Context, spec port.Spec) (*port.Status, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := portutil.ValidatePortSpec(spec, d.ports); err != nil {
		return nil, err
	}
	if spec.ChildIP != "" && spec.ChildIP != d.childIP {
		return nil, fmt.Errorf("unsupported ChildIP %q: the pesto port driver only supports the namespace address %q", spec.ChildIP, d.childIP)
	}

	opts, err := d.createArgs("-A", spec)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(d.binary, opts...)
	logrus.Debugf("Executing %v", cmd.Args)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("pesto failed: %w\noutput: %s", err, out)
	}

	id := d.nextID
	st := port.Status{
		ID:   id,
		Spec: spec,
	}
	d.ports[id] = &st
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

	st, ok := d.ports[id]
	if !ok {
		return fmt.Errorf("invalid ID: %d", id)
	}
	opts, err := d.createArgs("-D", st.Spec)
	if err != nil {
		return err
	}
	cmd := exec.Command(d.binary, opts...)
	logrus.Debugf("Executing %v", cmd.Args)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pesto failed: %w\noutput: %s", err, out)
	}
	delete(d.ports, id)

	return nil
}

func NewChildDriver() port.ChildDriver {
	return &childDriver{}
}

type childDriver struct {
}

func (d *childDriver) RunChildDriver(opaque map[string]string, quit <-chan struct{}, detachedNetNSPath string) error {
	// NOP
	<-quit
	return nil
}

// Available indicates whether this port driver is compiled in (used for generating help text)
const Available = true
