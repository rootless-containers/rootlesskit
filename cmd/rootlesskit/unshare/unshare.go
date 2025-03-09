package unshare

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/rootless-containers/rootlesskit/v2/pkg/common"
	"github.com/rootless-containers/rootlesskit/v2/pkg/version"
	"github.com/urfave/cli/v2"
)

func Main() {
	app := cli.NewApp()
	app.Name = "unshare"
	app.HideHelpCommand = true
	app.Version = version.Version
	app.Usage = "Reimplementation of unshare(1)"
	app.UsageText = "unshare [global options] [arguments...]"
	app.Flags = append(app.Flags, &cli.BoolFlag{
		Name:  "n,net",
		Usage: "unshare network namespace",
	})
	app.Action = action
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "[rootlesskit:unshare] error: %v\n", err)
		// propagate the exit code
		code, ok := common.GetExecExitStatus(err)
		if !ok {
			code = 1
		}
		os.Exit(code)
	}
}

func action(clicontext *cli.Context) error {
	ctx := clicontext.Context
	if clicontext.NArg() < 1 {
		return errors.New("no command specified")
	}
	cmdFlags := clicontext.Args().Slice()
	cmd := exec.CommandContext(ctx, cmdFlags[0], cmdFlags[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	if clicontext.Bool("n") {
		cmd.SysProcAttr.Cloneflags |= syscall.CLONE_NEWNET
	}
	return cmd.Run()
}
