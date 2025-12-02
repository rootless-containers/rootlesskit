//go:build !no_gvisortapvsock
// +build !no_gvisortapvsock

package gvisortapvsock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/containers/gvisor-tap-vsock/pkg/types"
	"github.com/rootless-containers/rootlesskit/v3/pkg/api"
	"github.com/rootless-containers/rootlesskit/v3/pkg/port"
	"github.com/sirupsen/logrus"
)

// NewParentDriver creates a new parent driver using gvisor-tap-vsock for port forwarding.
func NewParentDriver(logWriter io.Writer, stateDir string) (port.ParentDriver, error) {
	d := &driver{
		logWriter: logWriter,
		stateDir:  stateDir,
		ports:     make(map[int]*port.Status),
		portSeq:   1,
		ctx:       context.Background(),
	}
	return d, nil
}

// exposePort uses the HTTP API to expose a port
func (d *driver) exposePort(protocol types.TransportProtocol, local, remote string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.servicesMux == nil {
		return errors.New("services mux not initialized")
	}

	// Create a request to the PortsForwarder's HTTP API
	req := types.ExposeRequest{
		Protocol: protocol,
		Local:    local,
		Remote:   remote,
	}

	// Create a buffer to store the request body
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(req); err != nil {
		return fmt.Errorf("failed to encode request: %w", err)
	}

	u := fmt.Sprintf("http://%s/services/forwarder/expose", d.gatewayIP.String())
	httpReq, err := http.NewRequest(http.MethodPost, u, &buf)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Create a response recorder
	rr := httptest.NewRecorder()

	// Serve the request
	d.servicesMux.ServeHTTP(rr, httpReq)

	// Check the response
	if rr.Code != http.StatusOK {
		return fmt.Errorf("expose request failed with status %d: %s", rr.Code, rr.Body.String())
	}

	return nil
}

// unexposePort uses the HTTP API to unexpose a port
func (d *driver) unexposePort(protocol types.TransportProtocol, local string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.servicesMux == nil {
		return errors.New("services mux not initialized")
	}

	// Create a request to the PortsForwarder's HTTP API
	req := types.UnexposeRequest{
		Protocol: protocol,
		Local:    local,
	}

	// Create a buffer to store the request body
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(req); err != nil {
		return fmt.Errorf("failed to encode request: %w", err)
	}

	u := fmt.Sprintf("http://%s/services/forwarder/unexpose", d.gatewayIP.String())
	httpReq, err := http.NewRequest(http.MethodPost, u, &buf)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Create a response recorder
	rr := httptest.NewRecorder()

	// Serve the request
	d.servicesMux.ServeHTTP(rr, httpReq)

	// Check the response
	if rr.Code != http.StatusOK {
		return fmt.Errorf("unexpose request failed with status %d: %s", rr.Code, rr.Body.String())
	}

	return nil
}

func httpServe(ctx context.Context, g *errgroup.Group, ln net.Listener, mux http.Handler) {
	g.Go(func() error {
		<-ctx.Done()
		return ln.Close()
	})
	g.Go(func() error {
		s := &http.Server{
			Handler:      mux,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		}
		err := s.Serve(ln)
		if err != nil {
			if err != http.ErrServerClosed {
				return err
			}
			return err
		}
		return nil
	})
}

// NewChildDriver creates a new child driver for gvisor-tap-vsock port forwarding.
func NewChildDriver() port.ChildDriver {
	return &childDriver{}
}

type driver struct {
	logWriter   io.Writer
	stateDir    string
	mu          sync.Mutex
	ports       map[int]*port.Status
	portSeq     int
	ctx         context.Context
	cancel      context.CancelFunc
	childIP     string
	gatewayIP   net.IP
	servicesMux http.Handler
}

func (d *driver) Info(_ context.Context) (*api.PortDriverInfo, error) {
	return &api.PortDriverInfo{
		Driver: "gvisor-tap-vsock",
		// No additional options needed for this driver
		// as it uses the existing gvisor-tap-vsock network
	}, nil
}

func (d *driver) OpaqueForChild() map[string]string {
	return map[string]string{}
}

func (d *driver) RunParentDriver(initComplete chan struct{}, quit <-chan struct{}, cctx *port.ChildContext) error {
	if cctx == nil || cctx.IP == nil {
		return errors.New("child context IP is required")
	}

	d.childIP = cctx.IP.String()
	d.gatewayIP = cctx.GatewayIP

	d.mu.Lock()

	// Clean up existing virtual network if any
	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}

	// Create a new context with cancellation
	d.ctx, d.cancel = context.WithCancel(context.Background())

	// Get the virtual network from the child context
	if cctx != nil && cctx.Network != nil {
		d.servicesMux = cctx.Network.Mux()
		logrus.Debug("Using services mux from child context")
	}

	if d.servicesMux == nil {
		return fmt.Errorf("Virtual network services mux not available in child context")
	}

	d.mu.Unlock()
	logrus.Debug("Created virtual network")

	// Signal that initialization is complete
	close(initComplete)

	// Wait for quit signal
	<-quit

	// Cleanup
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}

	return nil
}

func (d *driver) AddPort(ctx context.Context, spec port.Spec) (*port.Status, error) {
	d.mu.Lock()

	// Validate protocol
	if !strings.HasPrefix(spec.Proto, "tcp") && !strings.HasPrefix(spec.Proto, "udp") {
		d.mu.Unlock()
		return nil, fmt.Errorf("unsupported protocol: %s", spec.Proto)
	}

	// Set gvisor-tap-vsock's child IP if not specified
	if spec.ChildIP == "" {
		spec.ChildIP = d.childIP
	}

	// Create port status
	id := d.portSeq
	d.portSeq++
	st := &port.Status{
		ID:   id,
		Spec: spec,
	}
	d.ports[id] = st

	// Format the local and remote addresses
	localAddr := fmt.Sprintf("%s:%d", spec.ParentIP, spec.ParentPort)
	remoteAddr := fmt.Sprintf("%s:%d", spec.ChildIP, spec.ChildPort)

	// Determine the protocol
	var protocol types.TransportProtocol = types.TCP
	if strings.HasPrefix(spec.Proto, "udp") {
		protocol = types.UDP
		// Add UDP prefix to the key for tracking in forwardsMap
		localAddr = "udp:" + localAddr
	}

	// Unlock before calling exposePort to avoid deadlock
	d.mu.Unlock()

	// For TCP, use the localAddr directly; for UDP, remove the "udp:" prefix
	exposeLocalAddr := localAddr
	if protocol == types.UDP {
		exposeLocalAddr = strings.TrimPrefix(localAddr, "udp:")
	}

	// Call exposePort to directly use the PortsForwarder without recreating the network
	err := d.exposePort(protocol, exposeLocalAddr, remoteAddr)

	if err != nil {
		// Need to lock again for cleanup on error
		d.mu.Lock()
		delete(d.ports, id)
		d.mu.Unlock()
		return nil, fmt.Errorf("failed to expose port: %w", err)
	}

	return st, nil
}

func (d *driver) ListPorts(ctx context.Context) ([]port.Status, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var ports []port.Status
	for _, p := range d.ports {
		ports = append(ports, *p)
	}
	return ports, nil
}

func (d *driver) RemovePort(ctx context.Context, id int) error {
	d.mu.Lock()

	st, ok := d.ports[id]
	if !ok {
		d.mu.Unlock()
		return fmt.Errorf("unknown port id: %d", id)
	}

	// Format the local address
	proto := st.Spec.Proto
	localAddr := fmt.Sprintf("%s:%d", st.Spec.ParentIP, st.Spec.ParentPort)

	// Determine the protocol
	var protocol types.TransportProtocol = types.TCP
	if strings.HasPrefix(proto, "udp") {
		protocol = types.UDP
		// Add UDP prefix to the key for tracking in forwardsMap
		localAddr = "udp:" + localAddr
	}

	// Remove from ports map
	delete(d.ports, id)

	// Unlock before calling unexposePort to avoid deadlock
	d.mu.Unlock()

	// For TCP, use the localAddr directly; for UDP, remove the "udp:" prefix
	unexposeLocalAddr := localAddr
	if protocol == types.UDP {
		unexposeLocalAddr = strings.TrimPrefix(localAddr, "udp:")
	}

	err := d.unexposePort(protocol, unexposeLocalAddr)

	if err != nil {
		logrus.Warnf("Failed to unexpose port %s: %v", localAddr, err)
		// We don't return the error here because the port is already removed from our tracking
		// and we don't want to block the removal operation due to API errors
	}

	return nil
}

type childDriver struct{}

func (d *childDriver) RunChildDriver(opaque map[string]string, quit <-chan struct{}, detachedNetNSPath string) error {
	// The child driver doesn't need to do anything special
	// as the gvisor-tap-vsock network driver handles the forwarding
	<-quit
	return nil
}

// Available indicates whether this port driver is compiled in (used for generating help text)
const Available = true
