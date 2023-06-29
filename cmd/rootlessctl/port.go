package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/urfave/cli/v2"

	"github.com/rootless-containers/rootlesskit/v2/pkg/port"
	"github.com/rootless-containers/rootlesskit/v2/pkg/port/portutil"
)

var listPortsCommand = cli.Command{
	Name:      "list-ports",
	Usage:     "List ports",
	ArgsUsage: "[flags]",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "json",
			Usage: "Prints as JSON",
		},
	},
	Action: listPortsAction,
}

func listPortsAction(clicontext *cli.Context) error {
	c, err := newClient(clicontext)
	if err != nil {
		return err
	}
	pm := c.PortManager()
	ctx := context.Background()
	portStatuses, err := pm.ListPorts(ctx)
	if err != nil {
		return err
	}
	if clicontext.Bool("json") {
		// Marshal per entry, for consistency with add-ports
		// (and for potential streaming support)
		for _, p := range portStatuses {
			m, err := json.Marshal(p)
			if err != nil {
				return err
			}
			fmt.Println(string(m))
		}
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 4, 8, 4, ' ', 0)
	if _, err := fmt.Fprintln(w, "ID\tPROTO\tPARENTIP\tPARENTPORT\tCHILDIP\tCHILDPORT\t"); err != nil {
		return err
	}
	for _, p := range portStatuses {
		if _, err := fmt.Fprintf(w, "%d\t%s\t%s\t%d\t%s\t%d\t\n",
			p.ID, p.Spec.Proto, p.Spec.ParentIP, p.Spec.ParentPort, p.Spec.ChildIP, p.Spec.ChildPort); err != nil {
			return err
		}
	}
	return w.Flush()
}

var addPortsCommand = cli.Command{
	Name:        "add-ports",
	Usage:       "Add ports",
	ArgsUsage:   "[flags] PARENTIP:PARENTPORT:CHILDPORT/PROTO [PARENTIP:PARENTPORT:CHILDPORT/PROTO...]",
	Description: "Add exposed ports. The port spec is similar to `docker run -p`. e.g. \"127.0.0.1:8080:80/tcp\".",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "json",
			Usage: "Prints as JSON",
		},
	},
	Action: addPortsAction,
}

func addPortsAction(clicontext *cli.Context) error {
	if clicontext.NArg() < 1 {
		return errors.New("no port specified")
	}
	var portSpecs []port.Spec
	for _, s := range clicontext.Args().Slice() {
		sp, err := portutil.ParsePortSpec(s)
		if err != nil {
			return err
		}
		portSpecs = append(portSpecs, *sp)
	}

	c, err := newClient(clicontext)
	if err != nil {
		return err
	}
	pm := c.PortManager()
	ctx := context.Background()
	for _, sp := range portSpecs {
		portStatus, err := pm.AddPort(ctx, sp)
		if err != nil {
			return err
		}
		if clicontext.Bool("json") {
			m, err := json.Marshal(portStatus)
			if err != nil {
				return err
			}
			fmt.Println(string(m))
		} else {
			fmt.Printf("%d\n", portStatus.ID)
		}
	}
	return nil
}

var removePortsCommand = cli.Command{
	Name:      "remove-ports",
	Usage:     "Remove ports",
	ArgsUsage: "[flags] ID [ID...]",
	Action:    removePortsAction,
}

func removePortsAction(clicontext *cli.Context) error {
	if clicontext.NArg() < 1 {
		return errors.New("no ID specified")
	}
	var ids []int
	for _, s := range clicontext.Args().Slice() {
		id, err := strconv.Atoi(s)
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}
	c, err := newClient(clicontext)
	if err != nil {
		return err
	}
	pm := c.PortManager()
	ctx := context.Background()
	for _, id := range ids {
		if err := pm.RemovePort(ctx, id); err != nil {
			return err
		}
		fmt.Printf("%d\n", id)
	}
	return nil
}
