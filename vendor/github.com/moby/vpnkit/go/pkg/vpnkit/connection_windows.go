package vpnkit

import (
	"context"
	"time"

	"github.com/Microsoft/go-winio"
	datakit "github.com/moby/datakit/api/go-datakit"
)

// Connection represents an open control connection to vpnkit
type Connection struct {
	client *datakit.Client
}

// NewConnection connects to a vpnkit Unix domain socket on the given path
// and returns the connection. If the path is the empty string then the
// default system path will be used.
func NewConnection(ctx context.Context, path string) (*Connection, error) {
	if path == "" {
		path = "//./pipe/dockerVpnKitControl"
	}
	timeout := time.Duration(30 * time.Second)
	conn, err := winio.DialPipe(path, &timeout)
	if err != nil {
		return nil, err
	}
	client, err := datakit.NewClient(ctx, conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return NewConnectionForClient(client), nil
}

// NewConnectionForClient returns a connection using given client
func NewConnectionForClient(client *datakit.Client) *Connection {
	return &Connection{client}
}
