// Package main provides the `rootlesskit-docker-proxy` binary (DEPRECATED)
// that was used by Docker prior to v28 for supporting rootless mode.
//
// The rootlesskit-docker-proxy binary is no longer needed since Docker v28,
// as the functionality of rootlesskit-docker-proxy is now provided by dockerd itself.
//
// https://github.com/moby/moby/pull/48132/commits/dac7ffa3404138a4f291c16586e5a2c68dad4151
//
// rootlesskit-docker-proxy will be removed in RootlessKit v3.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/rootless-containers/rootlesskit/v2/pkg/api"
	"github.com/rootless-containers/rootlesskit/v2/pkg/api/client"
	"github.com/rootless-containers/rootlesskit/v2/pkg/port"
	"github.com/sirupsen/logrus"
)

const (
	realProxy = "docker-proxy"
)

// drop-in replacement for docker-proxy.
// needs to be executed in the child namespace.
func main() {
	f := os.NewFile(3, "signal-parent")
	defer f.Close()
	if err := xmain(f); err != nil {
		// success: "0\n" (written by realProxy)
		// error: "1\n" (written by either rootlesskit-docker-proxy or realProxy)
		fmt.Fprintf(f, "1\n%s", err)
		log.Fatal(err)
	}
}

func isIPv6(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	return ip.To4() == nil
}

func getPortDriverProtos(info *api.Info) (string, map[string]struct{}, error) {
	if info.PortDriver == nil {
		return "", nil, errors.New("no port driver is available")
	}
	m := make(map[string]struct{}, len(info.PortDriver.Protos))
	for _, p := range info.PortDriver.Protos {
		m[p] = struct{}{}
	}
	return info.PortDriver.Driver, m, nil
}

type protocolUnsupportedError struct {
	apiProto       string
	portDriverName string
	hostIP         string
	hostPort       int
}

func (e *protocolUnsupportedError) Error() string {
	return fmt.Sprintf("protocol %q is not supported by the RootlessKit port driver %q, discarding request for %q",
		e.apiProto,
		e.portDriverName,
		net.JoinHostPort(e.hostIP, strconv.Itoa(e.hostPort)))
}

func callRootlessKitAPI(c client.Client, info *api.Info,
	hostIP string, hostPort int,
	dockerProxyProto, childIP string) (func() error, error) {
	// dockerProxyProto is like "tcp", but we need to convert it to "tcp4" or "tcp6" explicitly
	// for libnetwork >= 20201216
	//
	// See https://github.com/moby/libnetwork/pull/2604/files#diff-8fa48beed55dd033bf8e4f8c40b31cf69d0b2cc5d4bb53cde8594670ea6c938aR20
	// See also https://github.com/rootless-containers/rootlesskit/issues/231
	apiProto := dockerProxyProto
	if !strings.HasSuffix(apiProto, "4") && !strings.HasSuffix(apiProto, "6") {
		if isIPv6(hostIP) {
			apiProto += "6"
		} else {
			apiProto += "4"
		}
	}
	portDriverName, apiProtos, err := getPortDriverProtos(info)
	if err != nil {
		return nil, err
	}
	if _, ok := apiProtos[apiProto]; !ok {
		// This happens when apiProto="tcp6", portDriverName="slirp4netns",
		// because "slirp4netns" port driver does not support listening on IPv6 yet.
		//
		// Note that "slirp4netns" port driver is not used by default,
		// even when network driver is set to "slirp4netns".
		//
		// Most users are using "builtin" port driver and will not see this warning.
		err := &protocolUnsupportedError{
			apiProto:       apiProto,
			portDriverName: portDriverName,
			hostIP:         hostIP,
			hostPort:       hostPort,
		}
		return nil, err
	}

	pm := c.PortManager()
	p := port.Spec{
		Proto:      apiProto,
		ParentIP:   hostIP,
		ParentPort: hostPort,
		ChildIP:    childIP,
		ChildPort:  hostPort,
	}
	st, err := pm.AddPort(context.Background(), p)
	if err != nil {
		return nil, fmt.Errorf("error while calling PortManager.AddPort(): %w", err)
	}
	deferFunc := func() error {
		if dErr := pm.RemovePort(context.Background(), st.ID); dErr != nil {
			return fmt.Errorf("error while calling PortManager.RemovePort(): %w", err)
		}
		return nil
	}
	return deferFunc, nil
}

func xmain(f *os.File) error {
	containerIP := flag.String("container-ip", "", "container ip")
	containerPort := flag.Int("container-port", -1, "container port")
	hostIP := flag.String("host-ip", "", "host ip")
	hostPort := flag.Int("host-port", -1, "host port")
	proto := flag.String("proto", "tcp", "proxy protocol")
	flag.Parse()

	stateDir := os.Getenv("ROOTLESSKIT_STATE_DIR")
	if stateDir == "" {
		return errors.New("$ROOTLESSKIT_STATE_DIR needs to be set")
	}
	socketPath := filepath.Join(stateDir, "api.sock")
	c, err := client.New(socketPath)
	if err != nil {
		return fmt.Errorf("error while connecting to RootlessKit API socket: %w", err)
	}

	info, err := c.Info(context.Background())
	if err != nil {
		return fmt.Errorf("failed to call info API, probably RootlessKit binary is too old (needs to be v0.14.0 or later): %w", err)
	}

	// info.PortDriver is currently nil for "none" and "implicit", but this may change in future
	if info.PortDriver == nil || info.PortDriver.Driver == "none" || info.PortDriver.Driver == "implicit" {
		realProxyExe, err := exec.LookPath(realProxy)
		if err != nil {
			return err
		}
		return syscall.Exec(realProxyExe, append([]string{realProxy}, os.Args[1:]...), os.Environ())
	}

	// use loopback IP as the child IP, when port-driver="builtin"
	childIP := "127.0.0.1"
	if isIPv6(*hostIP) {
		childIP = "::1"
	}

	if info.PortDriver.DisallowLoopbackChildIP {
		// i.e., port-driver="slirp4netns"
		if info.NetworkDriver.ChildIP == nil {
			return fmt.Errorf("port driver (%q) does not allow loopback child IP, but network driver (%q) has no non-loopback IP",
				info.PortDriver.Driver, info.NetworkDriver.Driver)
		}
		childIP = info.NetworkDriver.ChildIP.String()
	}

	deferFunc, err := callRootlessKitAPI(c, info, *hostIP, *hostPort, *proto, childIP)
	if deferFunc != nil {
		defer func() {
			if dErr := deferFunc(); dErr != nil {
				logrus.Warn(dErr)
			}
		}()
	}
	if err != nil {
		if _, ok := err.(*protocolUnsupportedError); ok {
			logrus.Warn(err)
			// exit without executing realProxy (https://github.com/rootless-containers/rootlesskit/issues/250)
			fmt.Fprint(f, "0\n")
			return nil
		}
		return err
	}

	cmd := exec.Command(realProxy,
		"-container-ip", *containerIP,
		"-container-port", strconv.Itoa(*containerPort),
		"-host-ip", childIP,
		"-host-port", strconv.Itoa(*hostPort),
		"-proto", *proto)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	cmd.ExtraFiles = append(cmd.ExtraFiles, f)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("error while starting %s: %w", realProxy, err)
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	<-ch
	if err := cmd.Process.Kill(); err != nil {
		return fmt.Errorf("error while killing %s: %w", realProxy, err)
	}
	return nil
}
