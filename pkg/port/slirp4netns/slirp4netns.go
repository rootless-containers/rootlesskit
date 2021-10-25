package slirp4netns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/rootless-containers/rootlesskit/pkg/api"
	"github.com/rootless-containers/rootlesskit/pkg/port"
	"github.com/rootless-containers/rootlesskit/pkg/port/portutil"
)

func NewParentDriver(logWriter io.Writer, apiSocketPath string) (port.ParentDriver, error) {
	if apiSocketPath == "" {
		return nil, errors.New("api socket path is not set")
	}
	d := driver{
		logWriter:     logWriter,
		ports:         make(map[int]*port.Status, 0),
		apiSocketPath: apiSocketPath,
	}
	return &d, nil
}

type driver struct {
	logWriter     io.Writer
	apiSocketPath string
	mu            sync.Mutex
	childIP       string // can be empty
	ports         map[int]*port.Status
}

func (d *driver) Info(ctx context.Context) (*api.PortDriverInfo, error) {
	info := &api.PortDriverInfo{
		Driver: "slirp4netns",
		// No IPv6 support yet
		Protos:                  []string{"tcp", "tcp4", "udp", "udp4"},
		DisallowLoopbackChildIP: true,
	}
	return info, nil
}

func (d *driver) OpaqueForChild() map[string]string {
	// NOP, as this driver does not have child-side logic.
	return nil
}

func (d *driver) RunParentDriver(initComplete chan struct{}, quit <-chan struct{}, cctx *port.ChildContext) error {
	if cctx != nil && cctx.IP != nil && cctx.IP.To4() != nil {
		d.childIP = cctx.IP.To4().String()
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
	if strings.HasSuffix(spec.Proto, "6") {
		return nil, fmt.Errorf("unsupported protocol %q", spec.Proto)
	}
	proto := strings.TrimSuffix(spec.Proto, "4")
	ip := spec.ChildIP
	if ip == "" {
		ip = d.childIP
	} else {
		p := net.ParseIP(ip)
		if p == nil {
			return nil, fmt.Errorf("invalid IP: %q", ip)
		}
		p = p.To4()
		if p == nil {
			return nil, fmt.Errorf("unsupported IP (v6?): %s", ip)
		}
		ip = p.String()
	}
	req := request{
		Execute: "add_hostfwd",
		Arguments: addHostFwdArguments{
			Proto:     proto,
			HostAddr:  spec.ParentIP,
			HostPort:  spec.ParentPort,
			GuestAddr: ip,
			GuestPort: spec.ChildPort,
		},
	}
	rep, err := callAPI(d.apiSocketPath, req)
	if err != nil {
		return nil, err
	}
	if len(rep.Error) != 0 {
		return nil, fmt.Errorf("reply.Error: %+v", rep.Error)
	}
	idIntf, ok := rep.Return["id"]
	if !ok {
		return nil, fmt.Errorf("unexpected reply: %+v", rep)
	}
	idFloat, ok := idIntf.(float64)
	if !ok {
		return nil, fmt.Errorf("unexpected id: %+v", idIntf)
	}
	id := int(idFloat)
	st := port.Status{
		ID:   id,
		Spec: spec,
	}
	d.ports[id] = &st
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
	req := request{
		Execute: "remove_hostfwd",
		Arguments: removeHostFwdArguments{
			ID: id,
		},
	}
	rep, err := callAPI(d.apiSocketPath, req)
	if err != nil {
		return err
	}
	if len(rep.Error) != 0 {
		return fmt.Errorf("reply.Error: %v", rep.Error)
	}
	delete(d.ports, id)
	return nil
}

type addHostFwdArguments struct {
	Proto     string `json:"proto"`
	HostAddr  string `json:"host_addr"`
	HostPort  int    `json:"host_port"`
	GuestAddr string `json:"guest_addr"`
	GuestPort int    `json:"guest_port"`
}

type removeHostFwdArguments struct {
	ID int `json:"id"`
}

type request struct {
	Execute   string      `json:"execute"`
	Arguments interface{} `json:"arguments"`
}

type reply struct {
	Return map[string]interface{} `json:"return,omitempty"`
	Error  map[string]interface{} `json:"error,omitempty"`
}

func callAPI(apiSocketPath string, req request) (*reply, error) {
	addr := &net.UnixAddr{Net: "unix", Name: apiSocketPath}
	conn, err := net.DialUnix("unix", nil, addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, err
	}
	if err := conn.CloseWrite(); err != nil {
		return nil, err
	}
	b, err := io.ReadAll(conn)
	if err != nil {
		return nil, err
	}
	var rep reply
	if err := json.Unmarshal(b, &rep); err != nil {
		return nil, err
	}
	return &rep, nil
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
