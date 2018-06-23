// +build !windows

package vpnkit

import (
	"context"
	"errors"
	"os"
	"runtime"

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
		switch runtime.GOOS {
		case "darwin":
			path = os.Getenv("HOME") + "/Library/Containers/com.docker.docker/Data/s51"
		default:
			return nil, errors.New("path must be provided")
		}
	}
	client, err := datakit.Dial(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	return NewConnectionForClient(client), nil
}

// NewConnectionForClient returns a connection using given client
func NewConnectionForClient(client *datakit.Client) *Connection {
	return &Connection{client}
}
