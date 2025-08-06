package gvisortapvsock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/containers/gvisor-tap-vsock/pkg/types"
	"github.com/containers/gvisor-tap-vsock/pkg/virtualnetwork"
	"github.com/rootless-containers/rootlesskit/v2/pkg/api"
	"github.com/rootless-containers/rootlesskit/v2/pkg/port"
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

	if d.virtualNet == nil {
		return errors.New("virtual network not initialized")
	}

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

	// Create an HTTP request
	// The endpoint is /forwarder/expose as the handler is registered with a prefix
	httpReq, err := http.NewRequest(http.MethodPost, "/forwarder/expose", &buf)
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

	if d.virtualNet == nil {
		return errors.New("virtual network not initialized")
	}

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

	// Create an HTTP request
	// The endpoint is /forwarder/unexpose as the handler is registered with a prefix
	httpReq, err := http.NewRequest(http.MethodPost, "/forwarder/unexpose", &buf)
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
	virtualNet  *virtualnetwork.VirtualNetwork
	ctx         context.Context
	cancel      context.CancelFunc
	childIP     string
	servicesMux http.Handler
}

func (d *driver) Info(ctx context.Context) (*api.PortDriverInfo, error) {
	return &api.PortDriverInfo{
		Driver: "gvisortapvsock",
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

	d.mu.Lock()

	// Clean up existing virtual network if any
	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}

	// Create a new context with cancellation
	d.ctx, d.cancel = context.WithCancel(context.Background())

	// Create a new virtual network configuration
	config := &types.Configuration{
		Debug:     false,
		MTU:       1500,
		Subnet:    "192.168.127.0/24",      // Default subnet
		GatewayIP: "192.168.127.1",         // Default gateway
		Forwards:  make(map[string]string), // Empty forwards map, we'll use the HTTP API instead
	}

	// Add static lease for child IP if available
	if d.childIP != "" {
		config.DHCPStaticLeases = map[string]string{
			"02:42:ac:11:00:02": d.childIP,
		}
	}

	// Get the virtual network from the child context
	if cctx != nil && cctx.Network != nil {
		if vn, ok := cctx.Network.(*virtualnetwork.VirtualNetwork); ok {
			d.virtualNet = vn
			logrus.Debugf("Using virtual network from child context")
		}
	}

	if d.virtualNet == nil {
		return fmt.Errorf("Virtual network not available in child context")
	}

	// Get the services mux from the virtual network
	// This includes the PortsForwarder's HTTP API
	servicesMux := d.virtualNet.ServicesMux()

	// Extract the forwarder handler from the services mux
	// The forwarder handler is at /forwarder/ path
	forwarderHandler := http.StripPrefix("/forwarder", servicesMux)
	d.servicesMux = forwarderHandler

	d.mu.Unlock()
	logrus.Debugf("Created virtual network")

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

	// The VirtualNetwork struct doesn't have an explicit Close method,
	// but we'll keep a reference to it to prevent garbage collection until here
	// We don't need to clean up the network driver's virtual network as it's managed by the network driver
	d.virtualNet = nil

	return nil
}

func (d *driver) AddPort(ctx context.Context, spec port.Spec) (*port.Status, error) {
	d.mu.Lock()

	// Validate protocol
	proto := spec.Proto
	if !strings.HasPrefix(proto, "tcp") && !strings.HasPrefix(proto, "udp") {
		d.mu.Unlock()
		return nil, fmt.Errorf("unsupported protocol: %s", proto)
	}

	// Set default child IP if not specified
	childIP := spec.ChildIP
	if childIP == "" {
		childIP = "127.0.0.1" // Default to localhost in child namespace
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
	remoteAddr := fmt.Sprintf("%s:%d", childIP, spec.ChildPort)

	// Determine the protocol
	var protocol types.TransportProtocol = types.TCP
	if strings.HasPrefix(proto, "udp") {
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
