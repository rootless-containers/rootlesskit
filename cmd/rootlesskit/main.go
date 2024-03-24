package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Masterminds/semver/v3"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"

	"github.com/rootless-containers/rootlesskit/v2/pkg/child"
	"github.com/rootless-containers/rootlesskit/v2/pkg/common"
	"github.com/rootless-containers/rootlesskit/v2/pkg/copyup/tmpfssymlink"
	"github.com/rootless-containers/rootlesskit/v2/pkg/network/lxcusernic"
	"github.com/rootless-containers/rootlesskit/v2/pkg/network/pasta"
	"github.com/rootless-containers/rootlesskit/v2/pkg/network/slirp4netns"
	"github.com/rootless-containers/rootlesskit/v2/pkg/network/vpnkit"
	"github.com/rootless-containers/rootlesskit/v2/pkg/network/bridge"
	"github.com/rootless-containers/rootlesskit/v2/pkg/parent"
	"github.com/rootless-containers/rootlesskit/v2/pkg/port/builtin"
	"github.com/rootless-containers/rootlesskit/v2/pkg/port/portutil"
	slirp4netns_port "github.com/rootless-containers/rootlesskit/v2/pkg/port/slirp4netns"
	"github.com/rootless-containers/rootlesskit/v2/pkg/version"
)

func main() {
	const (
		pipeFDEnvKey     = "_ROOTLESSKIT_PIPEFD_UNDOCUMENTED"
		stateDirEnvKey   = "ROOTLESSKIT_STATE_DIR"   // documented
		parentEUIDEnvKey = "ROOTLESSKIT_PARENT_EUID" // documented
		parentEGIDEnvKey = "ROOTLESSKIT_PARENT_EGID" // documented
	)
	iAmChild := os.Getenv(pipeFDEnvKey) != ""
	id := "parent"
	if iAmChild {
		id = "child " // padded to len("parent")
	}
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

Note: RootlessKit requires /etc/subuid and /etc/subgid to be configured by the real root user.
See https://rootlesscontaine.rs/getting-started/common/ .
`
	app.Flags = []cli.Flag{
		Categorize(&cli.BoolFlag{
			Name:        "debug",
			Usage:       "debug mode",
			Destination: &debug,
		}, CategoryMisc),
		Categorize(&cli.StringFlag{
			Name:  "print-semver",
			Usage: "print a version component as a decimal integer [major, minor, patch]",
		}, CategoryMisc),
		Categorize(&cli.StringFlag{
			Name:  "state-dir",
			Usage: "state directory",
		}, CategoryState),
		Categorize(&cli.StringFlag{
			Name:  "net",
			Usage: "network driver [host, bridge, pasta(experimental), slirp4netns, vpnkit, lxc-user-nic(experimental)]",
			Value: "host",
		}, CategoryNetwork),
		Categorize(&cli.StringFlag{
			Name:  "pasta-binary",
			Usage: "path of pasta binary for --net=pasta",
			Value: "pasta",
		}, CategoryPasta),
		Categorize(&cli.StringFlag{
			Name:  "slirp4netns-binary",
			Usage: "path of slirp4netns binary for --net=slirp4netns",
			Value: "slirp4netns",
		}, CategorySlirp4netns),
		Categorize(&cli.StringFlag{
			Name:  "slirp4netns-sandbox",
			Usage: "enable slirp4netns sandbox (experimental) [auto, true, false] (the default is planned to be \"auto\" in future)",
			Value: "false",
		}, CategorySlirp4netns),
		Categorize(&cli.StringFlag{
			Name:  "slirp4netns-seccomp",
			Usage: "enable slirp4netns seccomp (experimental) [auto, true, false] (the default is planned to be \"auto\" in future)",
			Value: "false",
		}, CategorySlirp4netns),
		Categorize(&cli.StringFlag{
			Name:  "vpnkit-binary",
			Usage: "path of VPNKit binary for --net=vpnkit",
			Value: "vpnkit",
		}, CategoryVPNKit),
		Categorize(&cli.StringFlag{
			Name:  "lxc-user-nic-binary",
			Usage: "path of lxc-user-nic binary for --net=lxc-user-nic",
			Value: lxcUserNicBin(),
		}, CategoryLXCUserNic),
		Categorize(&cli.StringFlag{
			Name:  "lxc-user-nic-bridge",
			Usage: "lxc-user-nic bridge name",
			Value: "lxcbr0",
		}, CategoryLXCUserNic),
		Categorize(&cli.IntFlag{
			Name:  "mtu",
			Usage: "MTU for non-host network (default: 65520 for pasta and slirp4netns, 1500 for others)",
			Value: 0, // resolved into 65520 for slirp4netns, 1500 for others
		}, CategoryNetwork),
		Categorize(&cli.StringFlag{
			Name:  "cidr",
			Usage: "CIDR for pasta and slirp4netns networks (default: 10.0.2.0/24)",
		}, CategoryNetwork),
		Categorize(&cli.StringFlag{
			Name:  "ifname",
			Usage: "Network interface name (default: tap0 for pasta, slirp4netns, and vpnkit; eth0 for lxc-user-nic)",
		}, CategoryNetwork),
		Categorize(&cli.BoolFlag{
			Name:  "disable-host-loopback",
			Usage: "prohibit connecting to 127.0.0.1:* on the host namespace",
		}, CategoryNetwork),
		Categorize(&cli.BoolFlag{
			Name:  "ipv6",
			Usage: "enable IPv6 routing. Unrelated to port forwarding. Only supported for pasta and slirp4netns. (experimental)",
		}, CategoryNetwork),
		Categorize(&cli.StringSliceFlag{
			Name:  "copy-up",
			Usage: "mount a filesystem and copy-up the contents. e.g. \"--copy-up=/etc\" (typically required for non-host network)",
		}, CategoryMount),
		Categorize(&cli.StringFlag{
			Name:  "copy-up-mode",
			Usage: "copy-up mode [tmpfs+symlink]",
			Value: "tmpfs+symlink",
		}, CategoryMount),
		Categorize(&cli.StringFlag{
			Name:  "port-driver",
			Usage: "port driver for non-host network. [none, implicit (for pasta), builtin, slirp4netns]",
			Value: "none",
		}, CategoryPort),
		Categorize(&cli.StringSliceFlag{
			Name:    "publish",
			Aliases: []string{"p"},
			Usage:   "publish ports. e.g. \"127.0.0.1:8080:80/tcp\"",
		}, CategoryPort),
		Categorize(&cli.BoolFlag{
			Name:  "pidns",
			Usage: "create a PID namespace",
		}, CategoryProcess),
		Categorize(&cli.BoolFlag{
			Name:  "cgroupns",
			Usage: "create a cgroup namespace",
		}, CategoryProcess),
		Categorize(&cli.BoolFlag{
			Name:  "utsns",
			Usage: "create a UTS namespace",
		}, CategoryProcess),
		Categorize(&cli.BoolFlag{
			Name:  "ipcns",
			Usage: "create an IPC namespace",
		}, CategoryProcess),
		Categorize(&cli.BoolFlag{
			Name:  "detach-netns",
			Usage: "detach network namespaces ",
		}, CategoryNetwork),
		Categorize(&cli.StringFlag{
			Name:  "propagation",
			Usage: "mount propagation [rprivate, rslave]",
			Value: "rprivate",
		}, CategoryMount),
		Categorize(&cli.StringFlag{
			Name:  "reaper",
			Usage: "enable process reaper. Requires --pidns. [auto,true,false]",
			Value: "auto",
		}, CategoryProcess),
		Categorize(&cli.StringFlag{
			Name:  "evacuate-cgroup2",
			Usage: "evacuate processes into the specified subgroup. Requires --pidns and --cgroupns",
		}, CategoryProcess),
		Categorize(&cli.StringFlag{
			Name:  "subid-source",
			Value: "auto",
			Usage: "the source of the subids. \"dynamic\" executes /usr/bin/getsubids. \"static\" reads /etc/{subuid,subgid}. [auto,dynamic,static]",
		}, CategorySubID),
	}
	app.CustomAppHelpTemplate = `NAME:
   {{.Name}}{{if .Usage}} - {{.Usage}}{{end}}

USAGE:
   {{if .UsageText}}{{.UsageText}}{{else}}{{.HelpName}} {{if .VisibleFlags}}[global options]{{end}}{{if .Commands}} command [command options]{{end}} {{if .ArgsUsage}}{{.ArgsUsage}}{{else}}[arguments...]{{end}}{{end}}{{if .Version}}{{if not .HideVersion}}

VERSION:
   {{.Version}}{{end}}{{end}}{{if .Description}}

DESCRIPTION:
   {{.Description | nindent 3 | trim}}{{end}}

OPTIONS:
` + formatFlags(append(app.Flags,
		Categorize(cli.HelpFlag, CategoryMisc),
		Categorize(cli.VersionFlag, CategoryMisc)))

	app.Before = func(context *cli.Context) error {
		if debug {
			logrus.SetLevel(logrus.DebugLevel)
		}
		formatter := &logrusFormatter{
			id:        id,
			Formatter: logrus.StandardLogger().Formatter,
		}
		logrus.SetFormatter(formatter)

		return nil
	}
	app.Action = func(clicontext *cli.Context) error {
		if s := clicontext.String("print-semver"); s != "" {
			sv, err := semver.NewVersion(version.Version)
			if err != nil {
				return fmt.Errorf("failed to parse version %q: %w", version.Version, err)
			}
			switch s {
			case "major":
				fmt.Fprintln(clicontext.App.Writer, sv.Major())
			case "minor":
				fmt.Fprintln(clicontext.App.Writer, sv.Minor())
			case "patch":
				fmt.Fprintln(clicontext.App.Writer, sv.Patch())
			default:
				return fmt.Errorf("expected --print-semver=(major|minor|patch), got %q", s)
			}
			return nil
		}
		if clicontext.NArg() < 1 {
			return errors.New("no command specified")
		}
		if iAmChild {
			childOpt, err := createChildOpt(clicontext, pipeFDEnvKey, stateDirEnvKey, clicontext.Args().Slice())
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

type logrusFormatter struct {
	id string
	logrus.Formatter
}

func (f *logrusFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	entry.Message = fmt.Sprintf("[rootlesskit:%s] %s", f.id, entry.Message)
	return f.Formatter.Format(entry)
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
		return nil, fmt.Errorf("cidr must be like 10.0.2.0/24, not like 10.0.2.100/24")
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
		DetachNetNS:      clicontext.Bool("detach-netns"),
		ParentEUIDEnvKey: parentEUIDEnvKey,
		ParentEGIDEnvKey: parentEGIDEnvKey,
		Propagation:      clicontext.String("propagation"),
		EvacuateCgroup2:  clicontext.String("evacuate-cgroup2"),
		SubidSource:      parent.SubidSource(clicontext.String("subid-source")),
	}
	if opt.EvacuateCgroup2 != "" {
		if !opt.CreateCgroupNS {
			return opt, errors.New("evacuate-cgroup2 requires --cgroupns")
		}
		if !opt.CreatePIDNS {
			return opt, errors.New("evacuate-cgroup2 requires --pidns")
		}
	}
	opt.StateDir = clicontext.String("state-dir")
	if opt.StateDir == "" {
		opt.StateDir, err = os.MkdirTemp("", "rootlesskit")
		if err != nil {
			return opt, fmt.Errorf("creating a state directory: %w", err)
		}
	} else {
		opt.StateDir, err = filepath.Abs(opt.StateDir)
		if err != nil {
			return opt, err
		}
		if err := parent.InitStateDir(opt.StateDir); err != nil {
			return opt, err
		}
	}

	mtu := clicontext.Int("mtu")
	if mtu < 0 || mtu > 65521 {
		// 0 is ok (stands for the driver's default)
		return opt, fmt.Errorf("mtu must be <= 65521, got %d", mtu)
	}
	ipnet, err := parseCIDR(clicontext.String("cidr"))
	if err != nil {
		return opt, err
	}

	ifname := clicontext.String("ifname")
	if strings.Contains(ifname, "/") {
		return opt, errors.New("ifname must not contain \"/\"")
	}

	ipv6 := clicontext.Bool("ipv6")
	if ipv6 {
		logrus.Warn("ipv6 is experimental")
		if s := clicontext.String("net"); s != "pasta" && s != "slirp4netns" {
			logrus.Warnf("--ipv6 is discarded for --net=%s", s)
		}
	}

	disableHostLoopback := clicontext.Bool("disable-host-loopback")
	if !disableHostLoopback && clicontext.String("net") != "host" {
		logrus.Warn("specifying --disable-host-loopback is highly recommended to prohibit connecting to 127.0.0.1:* on the host namespace (requires pasta, slirp4netns, or VPNKit)")
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
			return opt, errors.New("custom cidr is not supported for --net=host")
		}
		if ifname != "" {
			return opt, errors.New("ifname cannot be specified for --net=host")
		}
	case "bridge":
		opt.NetworkDriver, err = bridge.NewParentDriver(mtu, ipnet, ifname)
		if err != nil {
			return opt, err
		}
	case "pasta":
		logrus.Warn("\"pasta\" network driver is experimental. Needs very recent version of pasta (see docs/network.md).")
		binary := clicontext.String("pasta-binary")
		if _, err := exec.LookPath(binary); err != nil {
			return opt, err
		}
		var implicitPortForward bool
		switch portDriver := clicontext.String("port-driver"); portDriver {
		case "none":
			implicitPortForward = false
		case "implicit":
			implicitPortForward = true
		default:
			return opt, errors.New("network \"pasta\" requires port driver \"none\" or \"implicit\"")
		}
		opt.NetworkDriver, err = pasta.NewParentDriver(&logrusDebugWriter{label: "network/pasta"}, binary, mtu, ipnet, ifname, disableHostLoopback, ipv6, implicitPortForward)
		if err != nil {
			return opt, err
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
			// Sandbox might not work when /etc/resolv.conf is a symlink to a file outside /etc or /run
			// https://github.com/rootless-containers/slirp4netns/issues/116

			// Sandbox is known to be incompatible with detach-netns
			// https://github.com/rootless-containers/slirp4netns/issues/317
			enableSandbox = features.SupportsEnableSandbox && !opt.DetachNetNS
		case "true":
			enableSandbox = true
			if !features.SupportsEnableSandbox {
				// NOTREACHED
				return opt, errors.New("unsupported slirp4netns version: lacks SupportsEnableSandbox")
			}
			if opt.DetachNetNS {
				return opt, errors.New("--slirp4netns-sandbox conflicts with --detach-netns (https://github.com/rootless-containers/slirp4netns/issues/317)")
			}
		case "false", "": // default
			// NOP
		default:
			return opt, fmt.Errorf("unsupported slirp4netns-sandbox mode: %q", s)
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
			return opt, fmt.Errorf("unsupported slirp4netns-seccomp mode: %q", s)
		}
		opt.NetworkDriver, err = slirp4netns.NewParentDriver(&logrusDebugWriter{label: "network/slirp4netns"}, binary, mtu, ipnet, ifname, disableHostLoopback, slirp4netnsAPISocketPath,
			enableSandbox, enableSeccomp, ipv6)
		if err != nil {
			return opt, err
		}
	case "vpnkit":
		if ipnet != nil {
			return opt, errors.New("custom cidr is not supported for --net=vpnkit")
		}
		binary := clicontext.String("vpnkit-binary")
		if _, err := exec.LookPath(binary); err != nil {
			return opt, err
		}
		opt.NetworkDriver = vpnkit.NewParentDriver(binary, mtu, ifname, disableHostLoopback)
	case "lxc-user-nic":
		logrus.Warn("\"lxc-user-nic\" network driver is experimental")
		if ipnet != nil {
			return opt, errors.New("custom cidr is not supported for --net=lxc-user-nic")
		}
		if !disableHostLoopback {
			logrus.Warn("--disable-host-loopback is implicitly set for lxc-user-nic")
		}
		binary := clicontext.String("lxc-user-nic-binary")
		if _, err := exec.LookPath(binary); err != nil {
			return opt, err
		}
		opt.NetworkDriver, err = lxcusernic.NewParentDriver(binary, mtu, clicontext.String("lxc-user-nic-bridge"), ifname)
		if err != nil {
			return opt, err
		}
	default:
		return opt, fmt.Errorf("unknown network mode: %s", s)
	}
	switch s := clicontext.String("port-driver"); s {
	case "none":
		// NOP
		if len(clicontext.StringSlice("publish")) != 0 {
			return opt, fmt.Errorf("port driver %q does not support publishing ports", s)
		}
	case "implicit":
		if clicontext.String("net") != "pasta" {
			return opt, errors.New("port driver requires pasta network")
		}
		// NOP
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
		return opt, fmt.Errorf("unknown port driver: %s", s)
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

func createChildOpt(clicontext *cli.Context, pipeFDEnvKey, stateDirEnvKey string, targetCmd []string) (child.Opt, error) {
	pidns := clicontext.Bool("pidns")
	detachNetNS := clicontext.Bool("detach-netns")
	opt := child.Opt{
		PipeFDEnvKey:    pipeFDEnvKey,
		StateDirEnvKey:  stateDirEnvKey,
		TargetCmd:       targetCmd,
		MountProcfs:     pidns,
		DetachNetNS:     detachNetNS,
		Propagation:     clicontext.String("propagation"),
		EvacuateCgroup2: clicontext.String("evacuate-cgroup2") != "",
	}
	switch reaperStr := clicontext.String("reaper"); reaperStr {
	case "auto":
		opt.Reaper = pidns
		logrus.Debugf("reaper: auto chosen value: %v", opt.Reaper)
	case "true":
		if !pidns {
			return opt, errors.New("reaper requires --pidns")
		}
		opt.Reaper = true
	case "false":
	default:
		return opt, fmt.Errorf("unknown reaper mode: %s", reaperStr)
	}
	switch s := clicontext.String("net"); s {
	case "host":
		// NOP
	case "bridge":
		opt.NetworkDriver = bridge.NewChildDriver()
	case "pasta":
		opt.NetworkDriver = pasta.NewChildDriver()
	case "slirp4netns":
		opt.NetworkDriver = slirp4netns.NewChildDriver()
	case "vpnkit":
		opt.NetworkDriver = vpnkit.NewChildDriver()
	case "lxc-user-nic":
		opt.NetworkDriver = lxcusernic.NewChildDriver()
	default:
		return opt, fmt.Errorf("unknown network mode: %s", s)
	}
	opt.CopyUpDirs = clicontext.StringSlice("copy-up")
	switch s := clicontext.String("copy-up-mode"); s {
	case "tmpfs+symlink":
		opt.CopyUpDriver = tmpfssymlink.NewChildDriver()
		if len(opt.CopyUpDirs) != 0 && (opt.Propagation == "rshared" || opt.Propagation == "shared") {
			return opt, fmt.Errorf("propagation %s does not support copy-up driver %s", opt.Propagation, s)
		}
	default:
		return opt, fmt.Errorf("unknown copy-up mode: %s", s)
	}
	switch s := clicontext.String("port-driver"); s {
	case "none", "implicit":
		// NOP
	case "slirp4netns":
		opt.PortDriver = slirp4netns_port.NewChildDriver()
	case "builtin":
		opt.PortDriver = builtin.NewChildDriver(&logrusDebugWriter{label: "port/builtin"})
	default:
		return opt, fmt.Errorf("unknown port driver: %s", s)
	}
	return opt, nil
}

func lxcUserNicBin() string {
	for _, path := range []string{
		"/usr/libexec/lxc/lxc-user-nic",                        // Debian, Fedora
		"/usr/lib/" + unameM() + "-linux-gnu/lxc/lxc-user-nic", // Ubuntu
		"/usr/lib/lxc/lxc-user-nic",                            // Arch Linux
	} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
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
