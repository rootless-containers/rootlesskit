//go:build !no_gvisortapvsock

package gvisortapvsock

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/containers/gvisor-tap-vsock/pkg/types"
)

type testTCPDialer struct {
	addr string
}

func (d testTCPDialer) DialContextTCP(ctx context.Context, addr string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "tcp", d.addr)
}

func TestTCPForward(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	local := listener.Addr().String()
	listener.Close()
	d := &driver{
		servicesMux: http.NewServeMux(),
		tcp:         make(map[string]io.Closer),
		tcpDialer:   testTCPDialer{addr: backend.Addr().String()},
	}
	if err := d.exposePort(types.TCP, local, "10.0.2.100:80"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, ok := d.tcp[local]; ok {
			d.unexposePort(types.TCP, local)
		}
	}()
	if err := d.exposePort(types.TCP, local, "10.0.2.100:80"); err == nil {
		t.Fatal("accepted duplicate listener")
	}

	request := bytes.Repeat([]byte("request"), 100000)
	response := bytes.Repeat([]byte("response"), 100000)
	done := make(chan error, 1)
	go func() {
		conn, err := backend.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		got, err := io.ReadAll(conn)
		if err == nil && !bytes.Equal(got, request) {
			err = io.ErrUnexpectedEOF
		}
		// Respond only after seeing the client's half-close.
		if err == nil {
			_, err = conn.Write(response)
		}
		done <- err
	}()
	client, err := net.DialTimeout("tcp", local, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := client.Write(request); err != nil {
		t.Fatal(err)
	}
	if err := client.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(client)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, response) {
		t.Fatal("response truncated or corrupted")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := d.unexposePort(types.TCP, local); err != nil {
		t.Fatal(err)
	}
	// Removal must release the listening socket.
	listener, err = net.Listen("tcp", local)
	if err != nil {
		t.Fatal(err)
	}
	listener.Close()
}
