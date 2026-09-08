//go:build !no_gvisortapvsock

package gvisortapvsock

import (
	"context"
	"errors"
	"io"
	"net"

	"github.com/inetaf/tcpproxy"
	"github.com/sirupsen/logrus"
)

// Larger copies let the virtual TCP stack fill packets at the default MTU
// (65520), instead of limiting writes to io.Copy's default 32 KiB buffer.
const tcpCopyBufferSize = 256 * 1024

type tcpDialer interface {
	DialContextTCP(context.Context, string) (net.Conn, error)
}

// exposeTCP is called with d.mu held, like the upstream expose API.
func (d *driver) exposeTCP(local, remote string) error {
	if d.tcpDialer == nil {
		return errors.New("virtual network TCP dialer is unavailable")
	}
	if _, ok := d.tcp[local]; ok {
		return errors.New("TCP proxy already running")
	}
	dialer := d.tcpDialer
	var proxy tcpproxy.Proxy
	proxy.AddRoute(local, &tcpproxy.DialProxy{
		Addr: remote,
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			conn, err := dialer.DialContextTCP(ctx, addr)
			if err != nil {
				return nil, err
			}
			return &bufferedTCPConn{Conn: conn}, nil
		},
	})
	if err := proxy.Start(); err != nil {
		return err
	}
	d.tcp[local] = &proxy
	go func() {
		if err := proxy.Wait(); err != nil {
			logrus.Debug(err)
		}
	}()
	return nil
}

// bufferedTCPConn supplies the proxy's copy buffers without adding another
// connection or relay. Forward half-closes to preserve TCP request/response EOFs.
type bufferedTCPConn struct{ net.Conn }

func (c *bufferedTCPConn) CloseRead() error {
	if conn, ok := c.Conn.(interface{ CloseRead() error }); ok {
		return conn.CloseRead()
	}
	return nil
}

func (c *bufferedTCPConn) CloseWrite() error {
	if conn, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return conn.CloseWrite()
	}
	return nil
}

func (c *bufferedTCPConn) ReadFrom(r io.Reader) (int64, error) {
	// Hide WriterTo/ReaderFrom to prevent io.CopyBuffer from bypassing our buffer.
	return io.CopyBuffer(struct{ io.Writer }{c.Conn}, struct{ io.Reader }{r}, make([]byte, tcpCopyBufferSize))
}

func (c *bufferedTCPConn) WriteTo(w io.Writer) (int64, error) {
	return io.CopyBuffer(struct{ io.Writer }{w}, struct{ io.Reader }{c.Conn}, make([]byte, tcpCopyBufferSize))
}
