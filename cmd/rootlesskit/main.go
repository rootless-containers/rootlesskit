package main

import (
	"fmt"
	"os"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"

	"github.com/AkihiroSuda/rootlesskit/pkg/child"
	"github.com/AkihiroSuda/rootlesskit/pkg/parent"
	"github.com/AkihiroSuda/rootlesskit/pkg/util"
)

func main() {
	pipeFDEnvKey := "_ROOTLESSKIT_PIPEFD_UNDOCUMENTED"
	iAmChild := os.Getenv(pipeFDEnvKey) != ""
	debug := false
	app := cli.NewApp()
	app.Name = "rootlesskit"
	app.Usage = "the gate to the rootless world"
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:        "debug",
			Usage:       "debug mode",
			Destination: &debug,
		},
	}
	app.Before = func(context *cli.Context) error {
		if debug {
			logrus.SetLevel(logrus.DebugLevel)
		}
		return nil
	}
	app.Action = func(clicontext *cli.Context) error {
		if clicontext.NArg() < 1 {
			return errors.New("no command specified")
		}
		magicPacket := []byte{0x42}
		if iAmChild {
			return child.Child(pipeFDEnvKey, magicPacket, clicontext.Args())
		}
		parentOpt, err := createParentOpt(clicontext)
		if err != nil {
			return err
		}
		return parent.Parent(pipeFDEnvKey, magicPacket, parentOpt)
	}
	if err := app.Run(os.Args); err != nil {
		id := "parent"
		if iAmChild {
			id = "child " // padded to len("parent")
		}
		if debug {
			fmt.Fprintf(os.Stderr, "[rootlesskit:%s] error: %+v\n", id, err)
		} else {
			fmt.Fprintf(os.Stderr, "[rootlesskit:%s] error: %v\n", id, err)
		}
		// propagate the exit code
		code, ok := util.GetExecExitStatus(err)
		if !ok {
			code = 1
		}
		os.Exit(code)
	}
}

func createParentOpt(clicontext *cli.Context) (*parent.Opt, error) {
	opt := &parent.Opt{}
	return opt, nil
}
