package main

import (
	"context"
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

	"github.com/rootless-containers/rootlesskit/pkg/api/client"
	"github.com/rootless-containers/rootlesskit/pkg/port"
	"github.com/sirupsen/logrus"

	"github.com/pkg/errors"
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

func getPortDriverProtos(c client.Client) (string, map[string]struct{}, error) {
	info, err := c.Info(context.Background())
	if err != nil {
		return "", nil, errors.Wrap(err, "failed to call info API, probably RootlessKit binary is too old (needs to be v0.14.0 or later)")
	}
	if info.PortDriver == nil {
		return "", nil, errors.New("no port driver is available")
	}
	m := make(map[string]struct{}, len(info.PortDriver.Protos))
	for _, p := range info.PortDriver.Protos {
		m[p] = struct{}{}
	}
	return info.PortDriver.Driver, m, nil
}

func callRootlessKitAPI(c client.Client,
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
	portDriverName, apiProtos, err := getPortDriverProtos(c)
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
		logrus.Warnf("protocol %q is not supported by the RootlessKit port driver %q, ignoring request for %q",
			apiProto,
			portDriverName,
			net.JoinHostPort(hostIP, strconv.Itoa(hostPort)))
		return nil, nil
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
		return nil, errors.Wrap(err, "error while calling PortManager.AddPort()")
	}
	deferFunc := func() error {
		if dErr := pm.RemovePort(context.Background(), st.ID); dErr != nil {
			return errors.Wrap(err, "error while calling PortManager.RemovePort()")
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
		return errors.Wrap(err, "error while connecting to RootlessKit API socket")
	}

	childIP := "127.0.0.1"
	if isIPv6(*hostIP) {
		childIP = "::1"
	}

	deferFunc, err := callRootlessKitAPI(c, *hostIP, *hostPort, *proto, childIP)
	if deferFunc != nil {
		defer func() {
			if dErr := deferFunc(); dErr != nil {
				logrus.Warn(dErr)
			}
		}()
	}
	if err != nil {
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
		return errors.Wrapf(err, "error while starting %s", realProxy)
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	<-ch
	if err := cmd.Process.Kill(); err != nil {
		return errors.Wrapf(err, "error while killing %s", realProxy)
	}
	return nil
}
