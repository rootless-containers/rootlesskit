package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/urfave/cli/v2"
)

var infoCommand = cli.Command{
	Name:      "info",
	Usage:     "Show info",
	ArgsUsage: "[flags]",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "json",
			Usage: "Prints as JSON",
		},
	},
	Action: infoAction,
}

func infoAction(clicontext *cli.Context) error {
	w := clicontext.App.Writer
	c, err := newClient(clicontext)
	if err != nil {
		return err
	}
	ctx := context.Background()
	info, err := c.Info(ctx)
	if err != nil {
		return err
	}
	if clicontext.Bool("json") {
		m, err := json.MarshalIndent(info, "", "    ")
		if err != nil {
			return err
		}
		fmt.Fprintln(w, string(m))
		return nil
	}
	fmt.Fprintf(w, "- REST API version: %s\n", info.APIVersion)
	fmt.Fprintf(w, "- Implementation version: %s\n", info.Version)
	fmt.Fprintf(w, "- State Directory: %s\n", info.StateDir)
	fmt.Fprintf(w, "- Child PID: %d\n", info.ChildPID)
	if info.NetworkDriver != nil {
		fmt.Fprintf(w, "- Network Driver: %s\n", info.NetworkDriver.Driver)
		fmt.Fprintf(w, "  - DNS: %v\n", info.NetworkDriver.DNS)
	}
	if info.PortDriver != nil {
		fmt.Fprintf(w, "- Port Driver: %s\n", info.PortDriver.Driver)
		fmt.Fprintf(w, "  - Supported protocols: %v\n", info.PortDriver.Protos)
	}
	return nil
}
