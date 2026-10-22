package tcp

import (
	"bytes"
	"errors"
	"net"
	"sync"
	"syscall"
	"testing"
	"time"
)

type acceptResult struct {
	conn net.Conn
	err  error
}

type testListener struct {
	results chan acceptResult
	closed  chan struct{}
	once    sync.Once
}

func newTestListener() *testListener {
	return &testListener{
		results: make(chan acceptResult),
		closed:  make(chan struct{}),
	}
}

func (l *testListener) Accept() (net.Conn, error) {
	select {
	case result := <-l.results:
		return result.conn, result.err
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *testListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *testListener) Addr() net.Addr {
	return &net.TCPAddr{}
}

type signalWriter struct {
	wrote chan struct{}
}

func (w signalWriter) Write(p []byte) (int, error) {
	select {
	case w.wrote <- struct{}{}:
	default:
	}
	return len(p), nil
}

func TestAcceptConnectionsRetriesFileDescriptorExhaustion(t *testing.T) {
	for _, acceptErr := range []error{syscall.EMFILE, syscall.ENFILE} {
		t.Run(acceptErr.Error(), func(t *testing.T) {
			listener := newTestListener()
			stopCh := make(chan struct{})
			newConns := make(chan net.Conn)
			done := make(chan struct{})
			var log bytes.Buffer

			go func() {
				acceptConnections(listener, newConns, stopCh, &log)
				close(done)
			}()

			listener.results <- acceptResult{err: acceptErr}
			serverConn, clientConn := net.Pipe()
			defer serverConn.Close()
			defer clientConn.Close()
			listener.results <- acceptResult{conn: serverConn}

			select {
			case conn := <-newConns:
				if conn != serverConn {
					t.Fatalf("received unexpected connection %v", conn)
				}
			case <-time.After(time.Second):
				t.Fatal("accept loop did not retry after descriptor exhaustion")
			}

			close(stopCh)
			listener.Close()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("accept loop did not stop")
			}
		})
	}
}

func TestAcceptConnectionsStopsDuringRetryDelay(t *testing.T) {
	listener := newTestListener()
	stopCh := make(chan struct{})
	newConns := make(chan net.Conn)
	done := make(chan struct{})
	wrote := make(chan struct{}, 1)

	go func() {
		acceptConnections(listener, newConns, stopCh, signalWriter{wrote: wrote})
		close(done)
	}()

	listener.results <- acceptResult{err: syscall.EMFILE}
	select {
	case <-wrote:
	case <-time.After(time.Second):
		t.Fatal("accept loop did not report descriptor exhaustion")
	}

	close(stopCh)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("accept loop did not stop during retry delay")
	}
	if conn, ok := <-newConns; ok {
		t.Fatalf("expected closed connection channel, got %v", conn)
	}
}

func TestAcceptConnectionsStopsOnPermanentError(t *testing.T) {
	listener := newTestListener()
	newConns := make(chan net.Conn)
	var log bytes.Buffer
	done := make(chan struct{})

	go func() {
		acceptConnections(listener, newConns, make(chan struct{}), &log)
		close(done)
	}()

	wantErr := errors.New("permanent accept error")
	listener.results <- acceptResult{err: wantErr}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("accept loop did not stop after a permanent error")
	}

	if got := <-newConns; got != nil {
		t.Fatalf("expected closed connection channel, got %v", got)
	}
	if !bytes.Contains(log.Bytes(), []byte(wantErr.Error())) {
		t.Fatalf("expected error in log, got %q", log.String())
	}
}
