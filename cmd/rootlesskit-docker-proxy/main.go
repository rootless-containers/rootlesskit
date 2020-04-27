package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/rootless-containers/rootlesskit/pkg/api/client"
	"github.com/rootless-containers/rootlesskit/pkg/port"

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
	pm := c.PortManager()
	p := port.Spec{
		Proto:      *proto,
		ParentIP:   *hostIP,
		ParentPort: *hostPort,
		ChildPort:  *hostPort,
	}
	st, err := pm.AddPort(context.Background(), p)
	if err != nil {
		return errors.Wrap(err, "error while calling PortManager.AddPort()")
	}
	defer pm.RemovePort(context.Background(), st.ID)

	cmd := exec.Command(realProxy,
		"-container-ip", *containerIP,
		"-container-port", strconv.Itoa(*containerPort),
		"-host-ip", "127.0.0.1",
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
