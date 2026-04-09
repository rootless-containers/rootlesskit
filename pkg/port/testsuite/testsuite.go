package testsuite

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
	"testing"
	"time"

	"github.com/rootless-containers/rootlesskit/v3/pkg/port"
)

const (
	reexecKeyMode     = "rootlesskit-port-testsuite.mode"
	reexecKeyOpaque   = "rootlesskit-port-testsuite.opaque"
	reexecKeyQuitFD   = "rootlesskit-port-testsuite.quitfd"
	reexecKeyEchoPort = "rootlesskit-port-testsuite.echoport"
)

func Main(m *testing.M, cf func() port.ChildDriver) {
	switch mode := os.Getenv(reexecKeyMode); mode {
	case "":
		os.Exit(m.Run())
	case "child":
	case "echoserver":
		runEchoServer()
		os.Exit(0)
	default:
		panic(fmt.Errorf("unknown mode: %q", mode))
	}
	var opaque map[string]string
	if err := json.Unmarshal([]byte(os.Getenv(reexecKeyOpaque)), &opaque); err != nil {
		panic(err)
	}
	quit := make(chan struct{})
	errCh := make(chan error)
	go func() {
		d := cf()
		dErr := d.RunChildDriver(opaque, quit, "")
		errCh <- dErr
	}()
	quitFD, err := strconv.Atoi(os.Getenv(reexecKeyQuitFD))
	if err != nil {
		panic(err)
	}
	quitR := os.NewFile(uintptr(quitFD), "")
	defer quitR.Close()
	if _, err = io.ReadAll(quitR); err != nil {
		panic(err)
	}
	quit <- struct{}{}
	err = <-errCh
	if err != nil {
		panic(err)
	}
	// when race detector is enabled, it takes about 1s after leaving from Main()
}

func Run(t *testing.T, pf func() port.ParentDriver) {
	RunTCP(t, pf)
	RunTCP4(t, pf)
	RunUDP(t, pf)
	RunUDP4(t, pf)
}

func RunTCP(t *testing.T, pf func() port.ParentDriver) {
	t.Run("TestTCP", func(t *testing.T) { TestProto(t, "tcp", pf()) })
}

func RunTCP4(t *testing.T, pf func() port.ParentDriver) {
	t.Run("TestTCP4", func(t *testing.T) { TestProto(t, "tcp4", pf()) })
}

func RunUDP(t *testing.T, pf func() port.ParentDriver) {
	t.Run("TestUDP", func(t *testing.T) { TestProto(t, "udp", pf()) })
}

func RunUDP4(t *testing.T, pf func() port.ParentDriver) {
	t.Run("TestUDP4", func(t *testing.T) { TestProto(t, "udp4", pf()) })
}

func TestProto(t *testing.T, proto string, d port.ParentDriver) {
	ensureDeps(t, "nsenter")
	t.Logf("creating USER+NET namespace")
	opaque := d.OpaqueForChild()
	opaqueJSON, err := json.Marshal(opaque)
	if err != nil {
		t.Fatal(err)
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/proc/self/exe")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Env = append([]string{
		reexecKeyMode + "=child",
		reexecKeyOpaque + "=" + string(opaqueJSON),
		reexecKeyQuitFD + "=3"}, os.Environ()...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig:  syscall.SIGKILL,
		Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWNET,
		UidMappings: []syscall.SysProcIDMap{
			{
				ContainerID: 0,
				HostID:      os.Geteuid(),
				Size:        1,
			},
		},
		GidMappings: []syscall.SysProcIDMap{
			{
				ContainerID: 0,
				HostID:      os.Getegid(),
				Size:        1,
			},
		},
	}
	cmd.ExtraFiles = []*os.File{pr}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		pw.Close()
		cmd.Wait()
	}()
	childPID := cmd.Process.Pid
	if out, err := nsenterExec(childPID, "ip", "link", "set", "lo", "up"); err != nil {
		t.Fatalf("%v, out=%s", err, string(out))
	}
	testProtoWithPID(t, proto, d, childPID)
}

func testProtoWithPID(t *testing.T, proto string, d port.ParentDriver, childPID int) {
	ensureDeps(t, "nsenter", "ip", "nc")
	// [child]parent
	pairs := make(map[int]int, 2)
	for _, childPort := range []int{80, 8080} {
		parentPort, err := allocateAvailablePort(proto)
		if err != nil {
			t.Fatalf("failed to allocate parent port for %s: %v", proto, err)
		}
		pairs[childPort] = parentPort
	}

	t.Logf("namespace pid: %d", childPID)
	initComplete := make(chan struct{})
	quit := make(chan struct{})
	driverErr := make(chan error)
	go func() {
		cctx := &port.ChildContext{
			IP: nil, // we don't have tap device in this test suite
		}
		driverErr <- d.RunParentDriver(initComplete, quit, cctx)
	}()
	select {
	case <-initComplete:
	case err := <-driverErr:
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for c, p := range pairs {
		childP, parentP := c, p
		wg.Add(1)
		go func() {
			testProtoRoutine(t, proto, d, childPID, childP, parentP)
			wg.Done()
		}()
	}
	wg.Wait()
	quit <- struct{}{}
	err := <-driverErr
	if err != nil {
		t.Fatal(err)
	}
}

func nsenterExec(pid int, cmdss ...string) ([]byte, error) {
	cmd := exec.Command("nsenter",
		append([]string{"-U", "--preserve-credential", "-n", "-t", strconv.Itoa(pid)},
			cmdss...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}
	return cmd.CombinedOutput()
}

// FIXME: support IPv6
func testProtoRoutine(t *testing.T, proto string, d port.ParentDriver, childPID, childP, parentP int) {
	stdoutR, stdoutW := io.Pipe()
	var ncFlags []string
	switch proto {
	case "tcp", "tcp4":
		// NOP
	case "udp", "udp4":
		ncFlags = append(ncFlags, "-u")
	default:
		panic("invalid proto")
	}
	cmd := exec.Command("nsenter", append(
		[]string{"-U", "--preserve-credential", "-n", "-t", strconv.Itoa(childPID),
			"nc"}, append(ncFlags, []string{"-l", strconv.Itoa(childP)}...)...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}
	cmd.Stdout = stdoutW
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		// NOTE: t.Fatal is not thread-safe while t.Error is (see godoc testing)
		panic(err)
	}
	defer cmd.Process.Kill()

	const maxAttempts = 10
	var (
		currentParent = parentP
		portStatus    *port.Status
		err           error
	)
	for attempt := 0; attempt < maxAttempts; attempt++ {
		portStatus, err = d.AddPort(context.TODO(),
			port.Spec{
				Proto:      proto,
				ParentIP:   "127.0.0.1",
				ParentPort: currentParent,
				ChildPort:  childP,
			})
		if err == nil {
			parentP = currentParent
			break
		}
		if attempt == maxAttempts-1 || !isAddrInUse(err) {
			panic(err)
		}
		currentParent, err = allocateAvailablePort(proto)
		if err != nil {
			panic(err)
		}
	}
	if portStatus == nil {
		panic("AddPort never succeeded")
	}
	defer func(id int) {
		if err := d.RemovePort(context.TODO(), id); err != nil {
			panic(err)
		}
		t.Logf("closed port ID %d", portStatus.ID)
	}(portStatus.ID)

	t.Logf("opened port: %+v", portStatus)
	if proto == "udp" || proto == "udp4" {
		// Dial does not return an error for UDP even if the port is not exposed yet
		time.Sleep(1 * time.Second)
	}
	var conn net.Conn
	for i := 0; i < 5; i++ {
		var dialer net.Dialer
		conn, err = dialer.Dial(proto, fmt.Sprintf("127.0.0.1:%d", parentP))
		if i == 4 && err != nil {
			panic(err)
		}
		if conn != nil && err == nil {
			break
		}
		time.Sleep(time.Duration(i*5) * time.Millisecond)
	}
	wBytes := []byte(fmt.Sprintf("test-%s-%d-%d-%d", proto, childPID, childP, parentP))
	if _, err := conn.Write(wBytes); err != nil {
		panic(err)
	}
	switch proto {
	case "tcp", "tcp4":
		if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
			panic(err)
		}
	case "udp", "udp4":
		if err := conn.(*net.UDPConn).Close(); err != nil {
			panic(err)
		}
	}
	rBytes := make([]byte, len(wBytes))
	if _, err := stdoutR.Read(rBytes); err != nil {
		panic(err)
	}
	if bytes.Compare(wBytes, rBytes) != 0 {
		panic(fmt.Errorf("expected %q, got %q", string(wBytes), string(rBytes)))
	}
	if proto == "tcp" || proto == "tcp4" {
		if err := conn.Close(); err != nil {
			panic(err)
		}
		if err := cmd.Wait(); err != nil {
			panic(err)
		}
	} else {
		// nc -u does not exit automatically
		syscall.Kill(cmd.Process.Pid, syscall.SIGKILL)
	}
}

func ensureDeps(t testing.TB, deps ...string) {
	for _, dep := range deps {
		if _, err := exec.LookPath(dep); err != nil {
			t.Skipf("%q not found: %v", dep, err)
		}
	}
}

func TLogWriter(t testing.TB, s string) io.Writer {
	return &tLogWriter{t: t, s: s}
}

type tLogWriter struct {
	t testing.TB
	s string
}

func (w *tLogWriter) Write(p []byte) (int, error) {
	w.t.Logf("%s: %s", w.s, strings.TrimSuffix(string(p), "\n"))
	return len(p), nil
}

// nonLoopbackIPv4 returns the first non-loopback IPv4 address found on the host.
func nonLoopbackIPv4() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ip4 := ipNet.IP.To4(); ip4 != nil {
				return ip4.String(), nil
			}
		}
	}
	return "", fmt.Errorf("no non-loopback IPv4 address found")
}

func allocateAvailablePort(proto string) (int, error) {
	const loopback = "127.0.0.1:0"
	switch proto {
	case "tcp", "tcp4":
		ln, err := net.Listen(proto, loopback)
		if err != nil {
			return 0, err
		}
		defer ln.Close()
		return ln.Addr().(*net.TCPAddr).Port, nil
	case "udp", "udp4":
		addr, err := net.ResolveUDPAddr(proto, loopback)
		if err != nil {
			return 0, err
		}
		conn, err := net.ListenUDP(proto, addr)
		if err != nil {
			return 0, err
		}
		defer conn.Close()
		return conn.LocalAddr().(*net.UDPAddr).Port, nil
	default:
		return 0, fmt.Errorf("unsupported proto %q", proto)
	}
}

func isAddrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "port is busy")
}

// runEchoServer is a re-exec mode that runs a minimal TCP server.
// It listens on 127.0.0.1:<port>, signals readiness by closing fd 3,
// accepts one connection, writes the remote address to stdout, and drains input.
func runEchoServer() {
	portStr := os.Getenv(reexecKeyEchoPort)
	if portStr == "" {
		panic("echoserver: missing " + reexecKeyEchoPort)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+portStr)
	if err != nil {
		panic(fmt.Errorf("echoserver: listen: %w", err))
	}
	defer ln.Close()
	// Signal readiness by closing fd 3
	readyW := os.NewFile(3, "ready")
	readyW.Close()

	conn, err := ln.Accept()
	if err != nil {
		panic(fmt.Errorf("echoserver: accept: %w", err))
	}
	defer conn.Close()
	fmt.Fprintln(os.Stdout, conn.RemoteAddr().String())
	io.Copy(io.Discard, conn)
}

func RunTCPTransparent(t *testing.T, pf func() port.ParentDriver) {
	t.Run("TestTCPTransparent", func(t *testing.T) { TestTCPTransparent(t, pf()) })
}

func TestTCPTransparent(t *testing.T, d port.ParentDriver) {
	ensureDeps(t, "nsenter")
	t.Logf("creating USER+NET namespace")
	opaque := d.OpaqueForChild()
	opaqueJSON, err := json.Marshal(opaque)
	if err != nil {
		t.Fatal(err)
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/proc/self/exe")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Env = append([]string{
		reexecKeyMode + "=child",
		reexecKeyOpaque + "=" + string(opaqueJSON),
		reexecKeyQuitFD + "=3"}, os.Environ()...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig:  syscall.SIGKILL,
		Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWNET,
		UidMappings: []syscall.SysProcIDMap{
			{
				ContainerID: 0,
				HostID:      os.Geteuid(),
				Size:        1,
			},
		},
		GidMappings: []syscall.SysProcIDMap{
			{
				ContainerID: 0,
				HostID:      os.Getegid(),
				Size:        1,
			},
		},
	}
	cmd.ExtraFiles = []*os.File{pr}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		pw.Close()
		cmd.Wait()
	}()
	childPID := cmd.Process.Pid
	if out, err := nsenterExec(childPID, "ip", "link", "set", "lo", "up"); err != nil {
		t.Fatalf("%v, out=%s", err, string(out))
	}
	testTCPTransparentWithPID(t, d, childPID)
}

func testTCPTransparentWithPID(t *testing.T, d port.ParentDriver, childPID int) {
	ensureDeps(t, "nsenter")
	const childPort = 80

	parentIP, err := nonLoopbackIPv4()
	if err != nil {
		t.Skip("no non-loopback IPv4 address available: ", err)
	}
	t.Logf("using non-loopback parent IP: %s", parentIP)

	// Start parent driver
	initComplete := make(chan struct{})
	quit := make(chan struct{})
	driverErr := make(chan error)
	go func() {
		cctx := &port.ChildContext{
			IP: nil,
		}
		driverErr <- d.RunParentDriver(initComplete, quit, cctx)
	}()
	select {
	case <-initComplete:
	case err := <-driverErr:
		t.Fatal(err)
	}

	// Start echo server inside the child namespace
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	// Pipe for readiness signaling (fd 3 in the echo server)
	readyR, readyW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	// Pipe for capturing stdout (the remote address)
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	echoCmd := exec.Command("nsenter", "-U", "--preserve-credential", "-n",
		"-t", strconv.Itoa(childPID),
		exe)
	echoCmd.Env = append([]string{
		reexecKeyMode + "=echoserver",
		reexecKeyEchoPort + "=" + strconv.Itoa(childPort),
	}, os.Environ()...)
	echoCmd.Stdout = stdoutW
	echoCmd.Stderr = os.Stderr
	echoCmd.ExtraFiles = []*os.File{readyW} // fd 3
	echoCmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}
	if err := echoCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer echoCmd.Process.Kill()
	readyW.Close()

	// Wait for echo server readiness
	io.ReadAll(readyR)
	readyR.Close()

	// Close the write end of stdout pipe in parent so reads see EOF when echo server exits
	stdoutW.Close()

	// Allocate a parent port and add port forwarding
	parentPort, err := allocateAvailablePort("tcp")
	if err != nil {
		t.Fatal(err)
	}

	var portStatus *port.Status
	const maxAttempts = 10
	for attempt := 0; attempt < maxAttempts; attempt++ {
		portStatus, err = d.AddPort(context.TODO(),
			port.Spec{
				Proto:      "tcp",
				ParentIP:   parentIP,
				ParentPort: parentPort,
				ChildPort:  childPort,
			})
		if err == nil {
			break
		}
		if attempt == maxAttempts-1 || !isAddrInUse(err) {
			t.Fatal(err)
		}
		parentPort, err = allocateAvailablePort("tcp")
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("opened port: %+v", portStatus)

	// Dial the parent port
	var conn net.Conn
	for i := 0; i < 5; i++ {
		var dialer net.Dialer
		conn, err = dialer.Dial("tcp", net.JoinHostPort(parentIP, strconv.Itoa(parentPort)))
		if err == nil {
			break
		}
		time.Sleep(time.Duration(i*5) * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}

	clientAddr := conn.LocalAddr().String()
	t.Logf("client local address: %s", clientAddr)

	// Send data and close write side
	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}

	// Read the remote address the echo server saw
	scanner := bufio.NewScanner(stdoutR)
	if !scanner.Scan() {
		t.Fatal("failed to read remote address from echo server")
	}
	serverSawAddr := scanner.Text()
	t.Logf("server saw remote address: %s", serverSawAddr)

	conn.Close()
	echoCmd.Wait()

	// Parse and verify: the echo server should see the client's non-loopback IP,
	// not 127.0.0.1 or a hard-coded router address.
	clientHost, _, err := net.SplitHostPort(clientAddr)
	if err != nil {
		t.Fatalf("failed to parse client address %q: %v", clientAddr, err)
	}
	serverHost, _, err := net.SplitHostPort(serverSawAddr)
	if err != nil {
		t.Fatalf("failed to parse server-seen address %q: %v", serverSawAddr, err)
	}

	if clientHost != serverHost {
		t.Errorf("IP mismatch: client=%s, server saw=%s", clientHost, serverHost)
	}

	// Cleanup
	if err := d.RemovePort(context.TODO(), portStatus.ID); err != nil {
		t.Fatal(err)
	}
	quit <- struct{}{}
	if err := <-driverErr; err != nil {
		t.Fatal(err)
	}
}
