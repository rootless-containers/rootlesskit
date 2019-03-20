package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/rootless-containers/rootlesskit/pkg/api/client"
	"github.com/rootless-containers/rootlesskit/pkg/port"
)

const (
	realProxy = "docker-proxy"
)

// drop-in replacement for docker-proxy.
// needs to be executed in the child namespace.
func main() {
	containerIP := flag.String("container-ip", "", "container ip")
	containerPort := flag.Int("container-port", -1, "container port")
	hostIP := flag.String("host-ip", "", "host ip")
	hostPort := flag.Int("host-port", -1, "host port")
	proto := flag.String("proto", "tcp", "proxy protocol")
	flag.Parse()

	cmd := exec.Command(realProxy,
		"-container-ip", *containerIP,
		"-container-port", strconv.Itoa(*containerPort),
		"-host-ip", "127.0.0.1",
		"-host-port", strconv.Itoa(*hostPort),
		"-proto", *proto)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}

	stateDir := os.Getenv("ROOTLESSKIT_STATE_DIR")
	if stateDir == "" {
		log.Fatalf("$ROOTLESSKIT_STATE_DIR needs to be set")
	}
	socketPath := filepath.Join(stateDir, "api.sock")
	c, err := client.New(socketPath)
	if err != nil {
		log.Fatal(err)
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
		log.Fatal(err)
	}
	defer pm.RemovePort(context.Background(), st.ID)

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	<-ch
	if err := cmd.Process.Kill(); err != nil {
		log.Fatal(err)
	}
}
