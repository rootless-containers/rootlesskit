package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"

	"github.com/rootless-containers/rootlesskit/v2/pkg/api/client"
	"github.com/rootless-containers/rootlesskit/v2/pkg/version"
)

func main() {
	debug := false
	app := cli.NewApp()
	app.Name = "rootlessctl"
	app.Version = version.Version
	app.Usage = "RootlessKit API client"
	app.Flags = []cli.Flag{
		&cli.BoolFlag{
			Name:        "debug",
			Usage:       "debug mode",
			Destination: &debug,
		},
		&cli.StringFlag{
			Name:  "socket",
			Usage: "Path to api.sock (under the \"rootlesskit --state-dir\" directory), defaults to $ROOTLESSKIT_STATE_DIR/api.sock",
		},
	}
	app.Commands = []*cli.Command{
		&listPortsCommand,
		&addPortsCommand,
		&removePortsCommand,
		&infoCommand,
	}
	app.Before = func(clicontext *cli.Context) error {
		if debug {
			logrus.SetLevel(logrus.DebugLevel)
		}
		return nil
	}
	if err := app.Run(os.Args); err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "error: %+v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
		os.Exit(1)
	}
}

func newClient(clicontext *cli.Context) (client.Client, error) {
	socketPath := clicontext.String("socket")
	if socketPath == "" {
		stateDir := os.Getenv("ROOTLESSKIT_STATE_DIR")
		if stateDir == "" {
			return nil, errors.New("please specify --socket or set $ROOTLESSKIT_STATE_DIR")
		}
		socketPath = filepath.Join(stateDir, "api.sock")
	}
	return client.New(socketPath)
}
