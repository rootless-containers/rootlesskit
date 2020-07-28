package main

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"

	"github.com/rootless-containers/rootlesskit/pkg/child"
	"github.com/rootless-containers/rootlesskit/pkg/common"
	"github.com/rootless-containers/rootlesskit/pkg/copyup/tmpfssymlink"
	"github.com/rootless-containers/rootlesskit/pkg/network/lxcusernic"
	"github.com/rootless-containers/rootlesskit/pkg/network/slirp4netns"
	"github.com/rootless-containers/rootlesskit/pkg/network/vpnkit"
	"github.com/rootless-containers/rootlesskit/pkg/parent"
	"github.com/rootless-containers/rootlesskit/pkg/port/builtin"
	"github.com/rootless-containers/rootlesskit/pkg/port/portutil"
	slirp4netns_port "github.com/rootless-containers/rootlesskit/pkg/port/slirp4netns"
	"github.com/rootless-containers/rootlesskit/pkg/port/socat"
	"github.com/rootless-containers/rootlesskit/pkg/version"
)

func main() {
	const (
		pipeFDEnvKey     = "_ROOTLESSKIT_PIPEFD_UNDOCUMENTED"
		stateDirEnvKey   = "ROOTLESSKIT_STATE_DIR"   // documented
		parentEUIDEnvKey = "ROOTLESSKIT_PARENT_EUID" // documented
		parentEGIDEnvKey = "ROOTLESSKIT_PARENT_EGID" // documented
	)
	iAmChild := os.Getenv(pipeFDEnvKey) != ""
	debug := false
	app := cli.NewApp()
	app.Name = "rootlesskit"
	app.Version = version.Version
	app.HideHelpCommand = true
	app.Usage = "Linux-native fakeroot using user namespaces"
	app.UsageText = "rootlesskit [global options] [arguments...]"
	app.Description = `RootlessKit is a Linux-native implementation of "fake root" using user_namespaces(7).

   Web site: https://github.com/rootless-containers/rootlesskit

   Examples:
     # spawn a shell with a new user namespace and a mount namespace
     rootlesskit bash

     # make /etc writable
     rootlesskit --copy-up=/etc bash

     # set mount propagation to rslave
     rootlesskit --propagation=rslave bash

     # create a network namespace with slirp4netns, and expose 80/tcp on the namespace as 8080/tcp on the host
     rootlesskit --copy-up=/etc --net=slirp4netns --disable-host-loopback --port-driver=builtin -p 127.0.0.1:8080:80/tcp bash

   Note: RootlessKit requires /etc/subuid and /etc/subgid to be configured by the real root user.`
	app.Flags = []cli.Flag{
		&cli.BoolFlag{
			Name:        "debug",
			Usage:       "debug mode",
			Destination: &debug,
		},
		&cli.StringFlag{
			Name:  "state-dir",
			Usage: "state directory",
		},
		&cli.StringFlag{
			Name:  "net",
			Usage: "network driver [host, slirp4netns, vpnkit, lxc-user-nic(experimental)]",
			Value: "host",
		},
		&cli.StringFlag{
			Name:  "slirp4netns-binary",
			Usage: "path of slirp4netns binary for --net=slirp4netns",
			Value: "slirp4netns",
		},
		&cli.StringFlag{
			Name:  "slirp4netns-sandbox",
			Usage: "enable slirp4netns sandbox (experimental) [auto, true, false] (the default is planned to be \"auto\" in future)",
			Value: "false",
		},
		&cli.StringFlag{
			Name:  "slirp4netns-seccomp",
			Usage: "enable slirp4netns seccomp (experimental) [auto, true, false] (the default is planned to be \"auto\" in future)",
			Value: "false",
		},
		&cli.StringFlag{
			Name:  "vpnkit-binary",
			Usage: "path of VPNKit binary for --net=vpnkit",
			Value: "vpnkit",
		},
		&cli.StringFlag{
			Name:  "lxc-user-nic-binary",
			Usage: "path of lxc-user-nic binary for --net=lxc-user-nic",
			Value: "/usr/lib/" + unameM() + "-linux-gnu/lxc/lxc-user-nic",
		},
		&cli.StringFlag{
			Name:  "lxc-user-nic-bridge",
			Usage: "lxc-user-nic bridge name",
			Value: "lxcbr0",
		},
		&cli.IntFlag{
			Name:  "mtu",
			Usage: "MTU for non-host network (default: 65520 for slirp4netns, 1500 for others)",
			Value: 0, // resolved into 65520 for slirp4netns, 1500 for others
		},
		&cli.StringFlag{
			Name:  "cidr",
			Usage: "CIDR for slirp4netns network (default: 10.0.2.0/24)",
		},
		&cli.BoolFlag{
			Name:  "disable-host-loopback",
			Usage: "prohibit connecting to 127.0.0.1:* on the host namespace",
		},
		&cli.StringSliceFlag{
			Name:  "copy-up",
			Usage: "mount a filesystem and copy-up the contents. e.g. \"--copy-up=/etc\" (typically required for non-host network)",
		},
		&cli.StringFlag{
			Name:  "copy-up-mode",
			Usage: "copy-up mode [tmpfs+symlink]",
			Value: "tmpfs+symlink",
		},
		&cli.StringFlag{
			Name:  "port-driver",
			Usage: "port driver for non-host network. [none, builtin, slirp4netns, socat(deprecated)]",
			Value: "none",
		},
		&cli.StringSliceFlag{
			Name:    "publish",
			Aliases: []string{"p"},
			Usage:   "publish ports. e.g. \"127.0.0.1:8080:80/tcp\"",
		},
		&cli.BoolFlag{
			Name:  "pidns",
			Usage: "create a PID namespace",
		},
		&cli.BoolFlag{
			Name:  "cgroupns",
			Usage: "create a cgroup namespace",
		},
		&cli.BoolFlag{
			Name:  "utsns",
			Usage: "create a UTS namespace",
		},
		&cli.BoolFlag{
			Name:  "ipcns",
			Usage: "create an IPC namespace",
		},
		&cli.StringFlag{
			Name:  "propagation",
			Usage: "mount propagation [rprivate, rslave]",
			Value: "rprivate",
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
		if iAmChild {
			childOpt, err := createChildOpt(clicontext, pipeFDEnvKey, clicontext.Args().Slice())
			if err != nil {
				return err
			}
			return child.Child(childOpt)
		}
		parentOpt, err := createParentOpt(clicontext, pipeFDEnvKey, stateDirEnvKey,
			parentEUIDEnvKey, parentEGIDEnvKey)
		if err != nil {
			return err
		}
		return parent.Parent(parentOpt)
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
		code, ok := common.GetExecExitStatus(err)
		if !ok {
			code = 1
		}
		os.Exit(code)
	}
}

func parseCIDR(s string) (*net.IPNet, error) {
	if s == "" {
		return nil, nil
	}
	ip, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		return nil, err
	}
	if !ip.Equal(ipnet.IP) {
		return nil, errors.Errorf("cidr must be like 10.0.2.0/24, not like 10.0.2.100/24")
	}
	return ipnet, nil
}

func createParentOpt(clicontext *cli.Context, pipeFDEnvKey, stateDirEnvKey, parentEUIDEnvKey, parentEGIDEnvKey string) (parent.Opt, error) {
	var err error
	opt := parent.Opt{
		PipeFDEnvKey:     pipeFDEnvKey,
		StateDirEnvKey:   stateDirEnvKey,
		CreatePIDNS:      clicontext.Bool("pidns"),
		CreateCgroupNS:   clicontext.Bool("cgroupns"),
		CreateUTSNS:      clicontext.Bool("utsns"),
		CreateIPCNS:      clicontext.Bool("ipcns"),
		ParentEUIDEnvKey: parentEUIDEnvKey,
		ParentEGIDEnvKey: parentEGIDEnvKey,
		Propagation:      clicontext.String("propagation"),
	}
	opt.StateDir = clicontext.String("state-dir")
	if opt.StateDir == "" {
		opt.StateDir, err = ioutil.TempDir("", "rootlesskit")
		if err != nil {
			return opt, errors.Wrap(err, "creating a state directory")
		}
	} else {
		opt.StateDir, err = filepath.Abs(opt.StateDir)
		if err != nil {
			return opt, err
		}
		if err = os.MkdirAll(opt.StateDir, 0755); err != nil {
			return opt, errors.Wrapf(err, "creating a state directory %s", opt.StateDir)
		}
	}

	mtu := clicontext.Int("mtu")
	if mtu < 0 || mtu > 65521 {
		// 0 is ok (stands for the driver's default)
		return opt, errors.Errorf("mtu must be <= 65521, got %d", mtu)
	}
	ipnet, err := parseCIDR(clicontext.String("cidr"))
	if err != nil {
		return opt, err
	}
	disableHostLoopback := clicontext.Bool("disable-host-loopback")
	if !disableHostLoopback && clicontext.String("net") != "host" {
		logrus.Warn("specifying --disable-host-loopback is highly recommended to prohibit connecting to 127.0.0.1:* on the host namespace (requires slirp4netns or VPNKit)")
	}

	slirp4netnsAPISocketPath := ""
	if clicontext.String("port-driver") == "slirp4netns" {
		slirp4netnsAPISocketPath = filepath.Join(opt.StateDir, ".s4nn.sock")
	}
	switch s := clicontext.String("net"); s {
	case "host":
		// NOP
		if mtu != 0 {
			logrus.Warnf("unsupported mtu for --net=host: %d", mtu)
		}
		if ipnet != nil {
			return opt, errors.New("custom cidr is supported only for --net=slirp4netns")
		}
	case "slirp4netns":
		binary := clicontext.String("slirp4netns-binary")
		if _, err := exec.LookPath(binary); err != nil {
			return opt, err
		}
		features, err := slirp4netns.DetectFeatures(binary)
		if err != nil {
			return opt, err
		}
		logrus.Debugf("slirp4netns features %+v", features)
		if disableHostLoopback && !features.SupportsDisableHostLoopback {
			// NOTREACHED
			return opt, errors.New("unsupported slirp4netns version: lacks SupportsDisableHostLoopback")
		}
		if slirp4netnsAPISocketPath != "" && !features.SupportsAPISocket {
			// NOTREACHED
			return opt, errors.New("unsupported slirp4netns version: lacks SupportsAPISocket")
		}
		enableSandbox := false
		switch s := clicontext.String("slirp4netns-sandbox"); s {
		case "auto":
			// this might not work when /etc/resolv.conf is a symlink to a file outside /etc or /run
			// https://github.com/rootless-containers/slirp4netns/issues/116
			enableSandbox = features.SupportsEnableSandbox
		case "true":
			enableSandbox = true
			if !features.SupportsEnableSandbox {
				// NOTREACHED
				return opt, errors.New("unsupported slirp4netns version: lacks SupportsEnableSandbox")
			}
		case "false", "": // default
			// NOP
		default:
			return opt, errors.Errorf("unsupported slirp4netns-sandbox mode: %q", s)
		}
		enableSeccomp := false
		switch s := clicontext.String("slirp4netns-seccomp"); s {
		case "auto":
			enableSeccomp = features.SupportsEnableSeccomp && features.KernelSupportsEnableSeccomp
		case "true":
			enableSeccomp = true
			if !features.SupportsEnableSeccomp {
				return opt, errors.New("unsupported slirp4netns version: lacks SupportsEnableSeccomp")
			}
			if !features.KernelSupportsEnableSeccomp {
				return opt, errors.New("kernel doesn't support seccomp")
			}
		case "false", "": // default
			// NOP
		default:
			return opt, errors.Errorf("unsupported slirp4netns-seccomp mode: %q", s)
		}
		opt.NetworkDriver, err = slirp4netns.NewParentDriver(&logrusDebugWriter{label: "network/slirp4netns"}, binary, mtu, ipnet, disableHostLoopback, slirp4netnsAPISocketPath, enableSandbox, enableSeccomp)
		if err != nil {
			return opt, err
		}
	case "vpnkit":
		if ipnet != nil {
			return opt, errors.New("custom cidr is supported only for --net=slirp4netns")
		}
		binary := clicontext.String("vpnkit-binary")
		if _, err := exec.LookPath(binary); err != nil {
			return opt, err
		}
		opt.NetworkDriver = vpnkit.NewParentDriver(binary, mtu, disableHostLoopback)
	case "lxc-user-nic":
		logrus.Warn("\"lxc-user-nic\" network driver is experimental")
		if ipnet != nil {
			return opt, errors.New("custom cidr is supported only for --net=slirp4netns")
		}
		if !disableHostLoopback {
			logrus.Warn("--disable-host-loopback is implicitly set for lxc-user-nic")
		}
		binary := clicontext.String("lxc-user-nic-binary")
		if _, err := exec.LookPath(binary); err != nil {
			return opt, err
		}
		opt.NetworkDriver, err = lxcusernic.NewParentDriver(binary, mtu, clicontext.String("lxc-user-nic-bridge"))
		if err != nil {
			return opt, err
		}
	default:
		return opt, errors.Errorf("unknown network mode: %s", s)
	}
	switch s := clicontext.String("port-driver"); s {
	case "none":
		// NOP
		if len(clicontext.StringSlice("publish")) != 0 {
			return opt, errors.Errorf("port driver %q does not support publishing ports", s)
		}
	case "socat":
		logrus.Warn("\"socat\" port driver is deprecated")
		if opt.NetworkDriver == nil {
			return opt, errors.New("port driver requires non-host network")
		}
		opt.PortDriver, err = socat.NewParentDriver(&logrusDebugWriter{label: "port/socat"})
		if err != nil {
			return opt, err
		}
	case "slirp4netns":
		if clicontext.String("net") != "slirp4netns" {
			return opt, errors.New("port driver requires slirp4netns network")
		}
		opt.PortDriver, err = slirp4netns_port.NewParentDriver(&logrusDebugWriter{label: "port/slirp4netns"}, slirp4netnsAPISocketPath)
		if err != nil {
			return opt, err
		}
	case "builtin":
		if opt.NetworkDriver == nil {
			return opt, errors.New("port driver requires non-host network")
		}
		opt.PortDriver, err = builtin.NewParentDriver(&logrusDebugWriter{label: "port/builtin"}, opt.StateDir)
		if err != nil {
			return opt, err
		}
	default:
		return opt, errors.Errorf("unknown port driver: %s", s)
	}
	for _, s := range clicontext.StringSlice("publish") {
		spec, err := portutil.ParsePortSpec(s)
		if err != nil {
			return opt, err
		}
		if err := portutil.ValidatePortSpec(*spec, nil); err != nil {
			return opt, err
		}
		opt.PublishPorts = append(opt.PublishPorts, *spec)
	}
	return opt, nil
}

type logrusDebugWriter struct {
	label string
}

func (w *logrusDebugWriter) Write(p []byte) (int, error) {
	s := strings.TrimSuffix(string(p), "\n")
	if w.label != "" {
		s = w.label + ": " + s
	}
	logrus.Debug(s)
	return len(p), nil
}

func createChildOpt(clicontext *cli.Context, pipeFDEnvKey string, targetCmd []string) (child.Opt, error) {
	opt := child.Opt{
		PipeFDEnvKey: pipeFDEnvKey,
		TargetCmd:    targetCmd,
		MountProcfs:  clicontext.Bool("pidns"),
		Propagation:  clicontext.String("propagation"),
		Reaper:       clicontext.Bool("pidns"),
	}
	switch s := clicontext.String("net"); s {
	case "host":
		// NOP
	case "slirp4netns":
		opt.NetworkDriver = slirp4netns.NewChildDriver()
	case "vpnkit":
		opt.NetworkDriver = vpnkit.NewChildDriver()
	case "lxc-user-nic":
		opt.NetworkDriver = lxcusernic.NewChildDriver()
	default:
		return opt, errors.Errorf("unknown network mode: %s", s)
	}
	opt.CopyUpDirs = clicontext.StringSlice("copy-up")
	switch s := clicontext.String("copy-up-mode"); s {
	case "tmpfs+symlink":
		opt.CopyUpDriver = tmpfssymlink.NewChildDriver()
		if len(opt.CopyUpDirs) != 0 && (opt.Propagation == "rshared" || opt.Propagation == "shared") {
			return opt, errors.Errorf("propagation %s does not support copy-up driver %s", opt.Propagation, s)
		}
	default:
		return opt, errors.Errorf("unknown copy-up mode: %s", s)
	}
	switch s := clicontext.String("port-driver"); s {
	case "none":
		// NOP
	case "socat":
		opt.PortDriver = socat.NewChildDriver()
	case "slirp4netns":
		opt.PortDriver = slirp4netns_port.NewChildDriver()
	case "builtin":
		opt.PortDriver = builtin.NewChildDriver(&logrusDebugWriter{label: "port/builtin"})
	default:
		return opt, errors.Errorf("unknown port driver: %s", s)
	}
	return opt, nil
}

func unameM() string {
	utsname := syscall.Utsname{}
	if err := syscall.Uname(&utsname); err != nil {
		panic(err)
	}
	var machine string
	for _, u8 := range utsname.Machine {
		if u8 != 0 {
			machine += string(byte(u8))
		}
	}
	return machine
}
