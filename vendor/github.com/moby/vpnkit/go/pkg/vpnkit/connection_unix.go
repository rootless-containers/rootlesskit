// +build !windows

package vpnkit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
		if runtime.GOOS == "darwin" {
			// The default path on the Mac has moved around a bit
			for _, possible := range []string{
				"s51",
				"vpnkit.port.sock",
			} {
				abs := filepath.Join(os.Getenv("HOME"), "Library", "Containers", "com.docker.docker", "Data", possible)
				if _, err := os.Stat(abs); err == nil {
					path = abs
					break
				}
			}
		}
		if path == "" {
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

var _ = Client(&Connection{})
