package testsuite

import (
	"bytes"
	"context"
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

	"github.com/rootless-containers/rootlesskit/pkg/port"
)

func Run(t *testing.T, df func() port.Driver) {
	t.Run("TestTCP", func(t *testing.T) { TestTCP(t, df()) })
}

func TestTCP(t *testing.T, d port.Driver) {
	ensureDeps(t, "nsenter")
	t.Logf("creating USER+NET namespace")
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("cat", "/dev/fd/3")
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
	TestTCPWithPID(t, d, childPID)
}

func TestTCPWithPID(t *testing.T, d port.Driver, childPID int) {
	ensureDeps(t, "nsenter", "ip", "nc")
	// [child]parent
	pairs := map[int]int{
		80:   42080,
		8080: 42880,
	}
	for _, parentPort := range pairs {
		var d net.Dialer
		d.Timeout = 50 * time.Millisecond
		_, err := d.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", parentPort))
		if err == nil {
			t.Fatalf("port %d is already used?", parentPort)
		}
	}

	t.Logf("namespace pid: %d", childPID)
	d.SetChildPID(childPID)
	var wg sync.WaitGroup
	for c, p := range pairs {
		childP, parentP := c, p
		wg.Add(1)
		go func() {
			testTCPRoutine(t, d, childPID, childP, parentP)
			wg.Done()
		}()
	}
	wg.Wait()
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

func testTCPRoutine(t *testing.T, d port.Driver, childPID, childP, parentP int) {
	stdoutR, stdoutW := io.Pipe()
	cmd := exec.Command("nsenter", "-U", "--preserve-credential", "-n", "-t", strconv.Itoa(childPID),
		"nc", "-l", strconv.Itoa(childP))
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
	portStatus, err := d.AddPort(context.TODO(),
		port.Spec{
			Proto:      "tcp",
			ParentIP:   "127.0.0.1",
			ParentPort: parentP,
			ChildPort:  childP,
		})
	if err != nil {
		panic(err)
	}
	t.Logf("opened port: %+v", portStatus)
	var conn net.Conn
	for i := 0; i < 5; i++ {
		var dialer net.Dialer
		conn, err = dialer.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", parentP))
		if i == 4 && err != nil {
			panic(err)
		}
		if conn != nil && err == nil {
			break
		}
		time.Sleep(time.Duration(i*5) * time.Millisecond)
	}
	wBytes := []byte(fmt.Sprintf("test-%d-%d-%d", childPID, childP, parentP))
	if _, err := conn.Write(wBytes); err != nil {
		panic(err)
	}
	if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
		panic(err)
	}
	rBytes := make([]byte, len(wBytes))
	if _, err := stdoutR.Read(rBytes); err != nil {
		panic(err)
	}
	if bytes.Compare(wBytes, rBytes) != 0 {
		panic(fmt.Errorf("expected %q, got %q", string(wBytes), string(rBytes)))
	}
	if err := conn.Close(); err != nil {
		panic(err)
	}
	if err := cmd.Wait(); err != nil {
		panic(err)
	}
	if err := d.RemovePort(context.TODO(), portStatus.ID); err != nil {
		panic(err)
	}
	t.Logf("closed port ID %d", portStatus.ID)
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
