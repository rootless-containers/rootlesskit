package gvisortapvsock

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containers/gvisor-tap-vsock/pkg/tap"
	"github.com/containers/gvisor-tap-vsock/pkg/types"
	"github.com/containers/gvisor-tap-vsock/pkg/virtualnetwork"
	"github.com/sirupsen/logrus"
	"github.com/songgao/water"

	"github.com/rootless-containers/rootlesskit/v2/pkg/api"
	"github.com/rootless-containers/rootlesskit/v2/pkg/common"
	"github.com/rootless-containers/rootlesskit/v2/pkg/messages"
	"github.com/rootless-containers/rootlesskit/v2/pkg/network"
	"github.com/rootless-containers/rootlesskit/v2/pkg/network/iputils"
	"github.com/rootless-containers/rootlesskit/v2/pkg/network/parentutils"
)

const (
	DriverName = "gvisor-tap-vsock"
	// Default buffer size for packet reading/writing
	defaultBufferSize = 65536
)

// NewParentDriver instantiates a new parent driver
func NewParentDriver(logWriter io.Writer, mtu int, ipnet *net.IPNet, ifname string, disableHostLoopback bool, enableIPv6 bool) (network.ParentDriver, error) {
	if mtu < 0 {
		return nil, errors.New("got negative mtu")
	}
	if mtu == 0 {
		mtu = 65520
	}

	if ifname == "" {
		ifname = "tap0"
	}

	return &parentDriver{
		logWriter:           logWriter,
		mtu:                 mtu,
		ipnet:               ipnet,
		ifname:              ifname,
		disableHostLoopback: disableHostLoopback,
		enableIPv6:          enableIPv6,
	}, nil
}

type parentDriver struct {
	logWriter           io.Writer
	mtu                 int
	ipnet               *net.IPNet
	ifname              string
	disableHostLoopback bool
	enableIPv6          bool
	infoMu              sync.RWMutex
	info                func() *api.NetworkDriverInfo

	// Store the virtual network and its network switch for later use
	vn            *virtualnetwork.VirtualNetwork
	networkSwitch *tap.Switch
	vnMu          sync.RWMutex

	// Socket for communication with the child namespace
	socketPath string
	listener   net.Listener
	ctx        context.Context
	cancel     context.CancelFunc
}

func (d *parentDriver) Info(ctx context.Context) (*api.NetworkDriverInfo, error) {
	d.infoMu.RLock()
	infoFn := d.info
	d.infoMu.RUnlock()
	if infoFn == nil {
		return &api.NetworkDriverInfo{
			Driver: DriverName,
		}, nil
	}

	return infoFn(), nil
}

func (d *parentDriver) MTU() int {
	return d.mtu
}

// setupNetworkConfig sets up the basic network configuration
func (d *parentDriver) setupNetworkConfig() (ip string, gateway string, netmask int, err error) {

	var ipAddr net.IP
	var gw net.IP

	ipAddr, err = iputils.AddIPInt(d.ipnet.IP, 100)
	if err != nil {
		return "", "", 0, err
	}
	ip = ipAddr.String()

	gw, err = iputils.AddIPInt(d.ipnet.IP, 1)
	if err != nil {
		return "", "", 0, err
	}
	gateway = gw.String()
	netmask, _ = d.ipnet.Mask.Size()

	return ip, gateway, netmask, nil
}

// setupVirtualNetwork creates and configures the virtual network
func (d *parentDriver) setupVirtualNetwork(gateway string) (*virtualnetwork.VirtualNetwork, error) {
	config := &types.Configuration{
		MTU:       d.mtu,
		Subnet:    d.ipnet.String(),
		GatewayIP: gateway,
		// This MAC address is a locally administered address (LAA) as indicated by the second least significant
		// bit of the first byte (5a). Using a fixed MAC address ensures consistent behavior across restarts
		// and allows for easier debugging and identification of the gateway interface.
		GatewayMacAddress: "5a:94:ef:e4:0c:dd",
		DHCPStaticLeases:  map[string]string{},
	}

	if !d.disableHostLoopback {
		// Configure NAT to allow access to host's localhost (127.0.0.1)
		// Map 10.0.2.1 (default gateway) to host's 127.0.0.1
		config.NAT = map[string]string{
			"10.0.2.1": "127.0.0.1",
		}
	}

	vn, err := virtualnetwork.New(config)
	if err != nil {
		return nil, fmt.Errorf("creating virtual network: %w", err)
	}

	return vn, nil
}

// setupDNSServers configures the DNS servers for the network
func (d *parentDriver) setupDNSServers() ([]string, error) {
	dns := make([]string, 0, 2)

	// Add IPv4 DNS server
	// dns server is bind to the gateway IP address
	// https://github.com/containers/gvisor-tap-vsock/blob/main/pkg/types/configuration.go#L29
	dnsIP, err := iputils.AddIPInt(d.ipnet.IP, 1)
	if err != nil {
		return nil, err
	}
	dns = append(dns, dnsIP.String())

	return dns, nil
}

// prepareNetworkMessage creates the network message with all configuration details
func (d *parentDriver) prepareNetworkMessage(virtualNetwork *virtualnetwork.VirtualNetwork, tap string, ip string, netmask int, gateway string) (*messages.ParentInitNetworkDriverCompleted, error) {
	dnsServers, err := d.setupDNSServers()
	if err != nil {
		return nil, err
	}

	netmsg := messages.ParentInitNetworkDriverCompleted{
		Network: virtualNetwork,
		Dev:     tap,
		DNS:     dnsServers,
		MTU:     d.mtu,
		IP:      ip,
		Netmask: netmask,
		Gateway: gateway,
	}

	return &netmsg, nil
}

// updateDriverInfo updates the driver info with network configuration
func (d *parentDriver) updateDriverInfo(netmsg *messages.ParentInitNetworkDriverCompleted) {
	apiDNS := make([]net.IP, 0, len(netmsg.DNS))
	for _, nameserver := range netmsg.DNS {
		apiDNS = append(apiDNS, net.ParseIP(nameserver))
	}

	d.infoMu.Lock()
	d.info = func() *api.NetworkDriverInfo {
		return &api.NetworkDriverInfo{
			Driver:         DriverName,
			DNS:            apiDNS,
			ChildIP:        net.ParseIP(netmsg.IP),
			DynamicChildIP: false,
		}
	}
	d.infoMu.Unlock()
}

// setupGvisortapvsockDir creates the directory for gvisor-tap-vsock files
func (d *parentDriver) setupGvisortapvsockDir(stateDir string) (string, error) {
	gvisortapvsockDir := filepath.Join(stateDir, "gvisortapvsock")
	if err := os.MkdirAll(gvisortapvsockDir, 0700); err != nil {
		return "", fmt.Errorf("creating gvisortapvsock directory: %w", err)
	}
	return gvisortapvsockDir, nil
}

// setupSocket creates a Unix socket for communication with the child namespace
func (d *parentDriver) setupSocket(gvisortapvsockDir string) error {
	d.socketPath = filepath.Join(gvisortapvsockDir, "tap.sock")

	if err := os.RemoveAll(d.socketPath); err != nil {
		return fmt.Errorf("removing existing socket: %w", err)
	}

	listener, err := net.Listen("unix", d.socketPath)
	if err != nil {
		return fmt.Errorf("creating unix socket: %w", err)
	}

	d.listener = listener
	d.ctx, d.cancel = context.WithCancel(context.Background())

	go d.acceptConnections()

	return nil
}

// acceptConnections accepts connections from the child namespace
func (d *parentDriver) acceptConnections() {
	for {
		select {
		case <-d.ctx.Done():
			return
		default:
			conn, err := d.listener.Accept()
			if err != nil {
				// Check if the error is due to the listener being closed, which is expected during cleanup
				if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "use of closed network connection") {
					logrus.Debugf("listener closed, stopping accept loop")
					return
				}
				logrus.Errorf("accepting connection: %v", err)
				continue
			}

			// Handle the connection
			go d.handleConnection(conn)
		}
	}
}

// handleConnection handles a connection from the child namespace
func (d *parentDriver) handleConnection(conn net.Conn) {
	defer conn.Close()

	d.vnMu.RLock()
	vn := d.vn
	d.vnMu.RUnlock()

	if vn == nil {
		logrus.Error("virtual network not initialized")
		return
	}

	// Use the AcceptStdio function to stream packets between the Unix socket and the virtual network
	// This will handle the connection and forward packets in both directions
	logrus.Debugf("forwarding packets between Unix socket and virtual network using VfkitProtocol")
	if err := vn.AcceptStdio(d.ctx, conn); err != nil {
		logrus.Debugf(err.Error())
		if errors.Is(err, io.EOF) {
			// This is expected when the child process exits
			logrus.Debugf("child process exited, connection closed: %v", err)
		} else {
			logrus.Errorf("accepting connection with VfkitProtocol: %v", err)
		}
	}
}

// createCleanupFunc creates a cleanup function for the virtual network
func (d *parentDriver) createCleanupFunc(vn *virtualnetwork.VirtualNetwork) func() error {
	return func() error {
		logrus.Debugf("closing gvisor-tap-vsock virtual network")
		// The VirtualNetwork struct doesn't have an explicit Close method,
		// but we'll keep a reference to it to prevent garbage collection
		_ = vn

		// Close the socket
		if d.cancel != nil {
			d.cancel()
		}
		if d.listener != nil {
			if err := d.listener.Close(); err != nil {
				logrus.Errorf("closing listener: %v", err)
			}
		}

		logrus.Debugf("closed gvisor-tap-vsock virtual network")
		return nil
	}
}

func (d *parentDriver) ConfigureNetwork(childPID int, stateDir, detachedNetNSPath string) (*messages.ParentInitNetworkDriverCompleted, func() error, error) {
	tap := d.ifname
	var cleanups []func() error

	// Set up the tap device
	if err := parentutils.PrepareTap(childPID, detachedNetNSPath, tap); err != nil {
		return nil, common.Seq(cleanups), fmt.Errorf("setting up tap %s: %w", tap, err)
	}

	// Set up network configuration
	ip, gateway, netmask, err := d.setupNetworkConfig()
	if err != nil {
		return nil, common.Seq(cleanups), err
	}

	// Create a directory for the gvisor-tap-vsock files
	gvisortapvsockDir, err := d.setupGvisortapvsockDir(stateDir)
	if err != nil {
		return nil, common.Seq(cleanups), err
	}

	// Set up the Unix socket
	if err := d.setupSocket(gvisortapvsockDir); err != nil {
		return nil, common.Seq(cleanups), fmt.Errorf("setting up socket: %w", err)
	}

	// Set up virtual network
	vn, err := d.setupVirtualNetwork(gateway)
	if err != nil {
		return nil, common.Seq(cleanups), err
	}

	// Store the virtual network for later use
	d.vnMu.Lock()
	d.vn = vn
	d.vnMu.Unlock()

	// Add cleanup function
	cleanups = append(cleanups, d.createCleanupFunc(vn))

	// Prepare network message
	netmsg, err := d.prepareNetworkMessage(vn, tap, ip, netmask, gateway)
	if err != nil {
		return nil, common.Seq(cleanups), err
	}

	// Add the socket path to the network driver opaque
	if netmsg.NetworkDriverOpaque == nil {
		netmsg.NetworkDriverOpaque = make(map[string]string)
	}
	netmsg.NetworkDriverOpaque["socketPath"] = d.socketPath

	// Update driver info
	d.updateDriverInfo(netmsg)

	return netmsg, common.Seq(cleanups), nil
}

func NewChildDriver() network.ChildDriver {
	return &childDriver{}
}

type childDriver struct {
	tap        *water.Interface
	conn       net.Conn
	socketPath string
	bufferPool *sync.Pool
}

func (d *childDriver) ChildDriverInfo() (*network.ChildDriverInfo, error) {
	return &network.ChildDriverInfo{
		ConfiguresInterface: false,
	}, nil
}

func (d *childDriver) ConfigureNetworkChild(netmsg *messages.ParentInitNetworkDriverCompleted, detachedNetNSPath string) (string, error) {
	tapName := netmsg.Dev
	if tapName == "" {
		return "", errors.New("could not determine the preconfigured tap")
	}

	d.socketPath = netmsg.NetworkDriverOpaque["socketPath"]
	if d.socketPath == "" {
		return "", errors.New("socket path not provided")
	}

	// Initialize buffer pool
	d.bufferPool = &sync.Pool{
		New: func() interface{} {
			return make([]byte, defaultBufferSize)
		},
	}

	fn := func(_ ns.NetNS) error {
		// Open the tap device
		var err error
		d.tap, err = water.New(water.Config{
			DeviceType: water.TAP,
			PlatformSpecificParams: water.PlatformSpecificParams{
				Name: tapName,
			},
		})
		if err != nil {
			return fmt.Errorf("opening tap device %s: %w", tapName, err)
		}

		var conn net.Conn
		conn, err = net.Dial("unix", d.socketPath)
		if err != nil {
			return fmt.Errorf("connecting to socket %s: %w", d.socketPath, err)
		}

		d.conn = conn

		// Start forwarding goroutines for direct streaming between tap and socket
		go d.forwardTapToSocket()
		go d.forwardSocketToTap()

		return nil
	}

	if detachedNetNSPath == "" {
		if err := fn(nil); err != nil {
			return "", err
		}
	} else {
		if err := ns.WithNetNSPath(detachedNetNSPath, fn); err != nil {
			return "", err
		}
	}

	// tap is created, opened, and "up".
	// IP stuff and MTU are not configured by the parent here,
	// and they are up to the child.
	return tapName, nil
}

// forwardTapToSocket forwards packets from the tap device to the Unix socket
func (d *childDriver) forwardTapToSocket() {
	size := make([]byte, 2)

	for {
		// Get buffer from pool
		bufInterface := d.bufferPool.Get()
		buf := bufInterface.([]byte)

		n, err := d.tap.Read(buf)
		if err != nil {
			// Return buffer to pool before exiting
			d.bufferPool.Put(buf)
			if err != io.EOF {
				logrus.Errorf("reading from tap: %v", err)
			}
			return
		}

		if n < 0 || n > math.MaxUint16 {
			// Return buffer to pool and continue
			d.bufferPool.Put(buf)
			logrus.Errorf("invalid frame length: %d", n)
			continue
		}

		// Encode size as 16-bit little-endian
		binary.LittleEndian.PutUint16(size, uint16(n))

		// Write size+packet to socket
		if _, err := d.conn.Write(append(size, buf[:n]...)); err != nil {
			// Return buffer to pool before exiting
			d.bufferPool.Put(buf)
			if err != io.EOF {
				logrus.Errorf("writing to socket: %v", err)
			}
			return
		}

		// Return buffer to pool for reuse
		d.bufferPool.Put(buf)
	}
}

// forwardSocketToTap forwards packets from the Unix socket to the tap device
func (d *childDriver) forwardSocketToTap() {
	sizeBuf := make([]byte, 2)

	for {
		_, err := io.ReadFull(d.conn, sizeBuf)
		if err != nil {
			if err != io.EOF {
				logrus.Errorf("reading size from socket: %v", err)
			}
			return
		}

		size := int(binary.LittleEndian.Uint16(sizeBuf))

		// Get buffer from pool
		bufInterface := d.bufferPool.Get()
		buf := bufInterface.([]byte)

		_, err = io.ReadFull(d.conn, buf[:size])
		if err != nil {
			// Return buffer to pool before exiting
			d.bufferPool.Put(buf)
			if err != io.EOF {
				logrus.Errorf("reading packet from socket: %v", err)
			}
			return
		}

		if _, err := d.tap.Write(buf[:size]); err != nil {
			// Return buffer to pool before exiting
			d.bufferPool.Put(buf)
			if err != io.EOF {
				logrus.Errorf("writing to tap: %v", err)
			}
			return
		}

		// Return buffer to pool for reuse
		d.bufferPool.Put(buf)
	}
}
