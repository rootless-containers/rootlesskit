package child

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/rootless-containers/rootlesskit/v3/pkg/lowlevelmsgutil"
	"github.com/rootless-containers/rootlesskit/v3/pkg/port"
	"github.com/rootless-containers/rootlesskit/v3/pkg/port/builtin/msg"
	opaquepkg "github.com/rootless-containers/rootlesskit/v3/pkg/port/builtin/opaque"
)

func NewDriver(logWriter io.Writer) port.ChildDriver {
	return &childDriver{
		logWriter: logWriter,
	}
}

type childDriver struct {
	logWriter           io.Writer
	sourceIPTransparent bool
	routingSetup        sync.Once
	routingReady        bool
	routingWarn         sync.Once
}

func (d *childDriver) RunChildDriver(opaque map[string]string, quit <-chan struct{}, detachedNetNSPath string) error {
	d.sourceIPTransparent = opaque[opaquepkg.SourceIPTransparent] == "true"
	socketPath := opaque[opaquepkg.SocketPath]
	if socketPath == "" {
		return errors.New("socket path not set")
	}
	childReadyPipePath := opaque[opaquepkg.ChildReadyPipePath]
	if childReadyPipePath == "" {
		return errors.New("child ready pipe path not set")
	}
	childReadyPipeW, err := os.OpenFile(childReadyPipePath, os.O_WRONLY, os.ModeNamedPipe)
	if err != nil {
		return err
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{
		Name: socketPath,
		Net:  "unix",
	})
	if err != nil {
		return err
	}
	// write nothing, just close
	if err = childReadyPipeW.Close(); err != nil {
		return err
	}
	stopAccept := make(chan struct{}, 1)
	go func() {
		<-quit
		stopAccept <- struct{}{}
		ln.Close()
	}()
	for {
		c, err := ln.AcceptUnix()
		if err != nil {
			select {
			case <-stopAccept:
				return nil
			default:
			}
			return err
		}
		go func() {
			if rerr := d.routine(c, detachedNetNSPath); rerr != nil {
				rep := msg.Reply{
					Error: rerr.Error(),
				}
				lowlevelmsgutil.MarshalToWriter(c, &rep)
			}
			c.Close()
		}()
	}
}

func (d *childDriver) routine(c *net.UnixConn, detachedNetNSPath string) error {
	var req msg.Request
	if _, err := lowlevelmsgutil.UnmarshalFromReader(c, &req); err != nil {
		return err
	}
	switch req.Type {
	case msg.RequestTypeInit:
		return d.handleConnectInit(c, &req)
	case msg.RequestTypeConnect:
		if detachedNetNSPath == "" {
			return d.handleConnectRequest(c, &req)
		} else {
			return ns.WithNetNSPath(detachedNetNSPath, func(_ ns.NetNS) error {
				return d.handleConnectRequest(c, &req)
			})
		}
	default:
		return fmt.Errorf("unknown request type %q", req.Type)
	}
}

func (d *childDriver) handleConnectInit(c *net.UnixConn, req *msg.Request) error {
	_, err := lowlevelmsgutil.MarshalToWriter(c, nil)
	return err
}

func (d *childDriver) handleConnectRequest(c *net.UnixConn, req *msg.Request) error {
	switch req.Proto {
	case "tcp":
	case "tcp4":
	case "tcp6":
	case "udp":
	case "udp4":
	case "udp6":
	default:
		return fmt.Errorf("unknown proto: %q", req.Proto)
	}
	// dialProto does not need "4", "6" suffix
	dialProto := strings.TrimSuffix(strings.TrimSuffix(req.Proto, "6"), "4")
	ip := req.IP
	if ip == "" {
		ip = "127.0.0.1"
		if req.ParentIP != "" {
			if req.ParentIP != req.HostGatewayIP && req.ParentIP != "0.0.0.0" {
				ip = req.ParentIP
			}
		}
	} else {
		p := net.ParseIP(ip)
		if p == nil {
			return fmt.Errorf("invalid IP: %q", ip)
		}
		ip = p.String()
	}
	targetAddr := net.JoinHostPort(ip, strconv.Itoa(req.Port))

	var targetConn net.Conn
	var err error
	if d.sourceIPTransparent && req.SourceIP != "" && req.SourcePort != 0 && dialProto == "tcp" && !net.ParseIP(req.SourceIP).IsLoopback() {
		d.routingSetup.Do(func() { d.routingReady = d.setupTransparentRouting() })
		if !d.routingReady {
			d.routingWarn.Do(func() {
				fmt.Fprintf(d.logWriter, "source IP transparent: falling back to non-transparent mode, client source IPs will not be preserved\n")
			})
			goto fallback
		}
		targetConn, err = transparentDial(dialProto, targetAddr, req.SourceIP, req.SourcePort)
		if err != nil {
			fmt.Fprintf(d.logWriter, "transparent dial failed, falling back: %v\n", err)
			targetConn, err = nil, nil
		}
	}
fallback:
	if targetConn == nil {
		var dialer net.Dialer
		targetConn, err = dialer.Dial(dialProto, targetAddr)
		if err != nil {
			return err
		}
	}
	defer targetConn.Close() // no effect on duplicated FD
	targetConnFiler, ok := targetConn.(filer)
	if !ok {
		return fmt.Errorf("unknown target connection: %+v", targetConn)
	}
	targetConnFile, err := targetConnFiler.File()
	if err != nil {
		return err
	}
	defer targetConnFile.Close()
	oob := unix.UnixRights(int(targetConnFile.Fd()))
	f, err := c.File()
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		err = unix.Sendmsg(int(f.Fd()), []byte("dummy"), oob, nil, 0)
		if err != unix.EINTR {
			break
		}
	}
	return err
}

// setupTransparentRouting sets up policy routing so that response packets
// destined to transparent-bound source IPs are delivered locally.
//
// Transparent sockets (IP_TRANSPARENT) bind to non-local addresses (the real
// client IP). Response packets to these addresses must be routed locally instead
// of being sent out through the TAP device (slirp4netns).
//
// The transparent SYN goes through OUTPUT (where we tag it with CONNMARK) and
// then either:
//
//  1. Gets DNAT'd to the container (nerdctl/CNI): the SYN-ACK arrives via the
//     bridge in PREROUTING, where we restore connmark to fwmark.
//
//  2. Goes through loopback to a userspace proxy like docker-proxy: the SYN
//     enters PREROUTING on loopback with connmark, which sets fwmark. With
//     tcp_fwmark_accept=1, the accepted socket inherits the fwmark. The proxy's
//     SYN-ACK is then routed via the fwmark table (local delivery) instead of
//     the default route (TAP), allowing it to reach the transparent socket.
func (d *childDriver) setupTransparentRouting() bool {
	// Check that iptables is available before proceeding.
	if _, err := exec.LookPath("iptables"); err != nil {
		fmt.Fprintf(d.logWriter, "source IP transparent: iptables not found, disabling: %v\n", err)
		return false
	}
	// Verify the connmark module is usable (kernel module might not be loaded).
	if out, err := exec.Command("iptables", "-t", "mangle", "-L", "-n").CombinedOutput(); err != nil {
		fmt.Fprintf(d.logWriter, "source IP transparent: iptables mangle table not available, disabling: %v: %s\n", err, out)
		return false
	}
	cmds := [][]string{
		// Table 100: treat all addresses as local (for delivery to transparent sockets)
		{"ip", "route", "add", "local", "default", "dev", "lo", "table", "100"},
		{"ip", "-6", "route", "add", "local", "default", "dev", "lo", "table", "100"},
		// Route fwmark-100 packets via table 100
		{"ip", "rule", "add", "fwmark", "100", "lookup", "100", "priority", "100"},
		{"ip", "-6", "rule", "add", "fwmark", "100", "lookup", "100", "priority", "100"},
		// Inherit fwmark from SYN to accepted socket (needed for userspace proxies
		// like docker-proxy, so that SYN-ACK routing uses table 100)
		{"sysctl", "-w", "net.ipv4.tcp_fwmark_accept=1"},
		// In OUTPUT: tag transparent connections (non-local source) with CONNMARK
		{"iptables", "-t", "mangle", "-A", "OUTPUT", "-p", "tcp", "-m", "addrtype", "!", "--src-type", "LOCAL", "-j", "CONNMARK", "--set-mark", "100"},
		{"ip6tables", "-t", "mangle", "-A", "OUTPUT", "-p", "tcp", "-m", "addrtype", "!", "--src-type", "LOCAL", "-j", "CONNMARK", "--set-mark", "100"},
		// In PREROUTING: restore connmark to fwmark for routing
		{"iptables", "-t", "mangle", "-A", "PREROUTING", "-p", "tcp", "-m", "connmark", "--mark", "100", "-j", "MARK", "--set-mark", "100"},
		{"ip6tables", "-t", "mangle", "-A", "PREROUTING", "-p", "tcp", "-m", "connmark", "--mark", "100", "-j", "MARK", "--set-mark", "100"},
	}
	for _, args := range cmds {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			fmt.Fprintf(d.logWriter, "source IP transparent routing setup: %v: %s\n", err, out)
		}
	}
	return true
}

// transparentDial dials targetAddr using IP_TRANSPARENT, binding to the given
// source IP and port so the backend service sees the real client address.
func transparentDial(dialProto, targetAddr, sourceIP string, sourcePort int) (net.Conn, error) {
	dialer := net.Dialer{
		Timeout:   time.Second,
		LocalAddr: &net.TCPAddr{IP: net.ParseIP(sourceIP), Port: sourcePort},
		Control: func(network, address string, c syscall.RawConn) error {
			var sockErr error
			if err := c.Control(func(fd uintptr) {
				if strings.Contains(network, "6") {
					sockErr = unix.SetsockoptInt(int(fd), unix.SOL_IPV6, unix.IPV6_TRANSPARENT, 1)
				} else {
					sockErr = unix.SetsockoptInt(int(fd), unix.SOL_IP, unix.IP_TRANSPARENT, 1)
				}
			}); err != nil {
				return err
			}
			return sockErr
		},
	}
	return dialer.Dial(dialProto, targetAddr)
}

// filer is implemented by *net.TCPConn and *net.UDPConn
type filer interface {
	File() (f *os.File, err error)
}
