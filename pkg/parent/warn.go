package parent

import (
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/moby/sys/mountinfo"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

func warnPropagation(propagation string) {
	mounts, err := mountinfo.GetMounts(mountinfo.SingleEntryFilter("/"))
	if err != nil || len(mounts) < 1 {
		logrus.WithError(err).Warn("Failed to parse mountinfo")
		return
	}
	root := mounts[0]
	// 1. When running on a "sane" host,   root.Optional is like "shared:1".   ("shared" in findmnt(8) output)
	// 2. When running inside a container, root.Optional is like "master:363". ("private, slave" in findmnt(8) output)
	//
	// Setting non-private propagation is supported for 1, unsupported for 2.
	if !strings.Contains(propagation, "private") && !strings.Contains(root.Optional, "shared") {
		logrus.Warnf("The host root filesystem is mounted as %q. Setting child propagation to %q is not supported.",
			root.Optional, propagation)
	}
}

// warnSysctl verifies /proc/sys/kernel/unprivileged_userns_clone and /proc/sys/user/max_user_namespaces
func warnSysctl() {
	uuc, err := os.ReadFile("/proc/sys/kernel/unprivileged_userns_clone")
	// The file exists only on distros with the "add sysctl to disallow unprivileged CLONE_NEWUSER by default" patch.
	// (e.g. Debian and Arch)
	if err == nil {
		s := strings.TrimSpace(string(uuc))
		i, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			logrus.WithError(err).Warnf("Failed to parse /proc/sys/kernel/unprivileged_userns_clone (%q)", s)
		} else if i == 0 {
			logrus.Warn("/proc/sys/kernel/unprivileged_userns_clone needs to be set to 1.")
		}
	}

	mun, err := os.ReadFile("/proc/sys/user/max_user_namespaces")
	if err == nil {
		s := strings.TrimSpace(string(mun))
		i, err := strconv.ParseInt(strings.TrimSpace(string(mun)), 10, 64)
		if err != nil {
			logrus.WithError(err).Warnf("Failed to parse /proc/sys/user/max_user_namespaces (%q)", s)
		} else if i == 0 {
			logrus.Warn("/proc/sys/user/max_user_namespaces needs to be set to non-zero.")
		} else {
			threshold := int64(1024)
			if i < threshold {
				logrus.Warnf("/proc/sys/user/max_user_namespaces=%d may be low. Consider setting to >= %d.", i, threshold)
			}
		}
	}
}

func warnOnChildStartFailure(childStartErr error) {
	if errors.Is(childStartErr, unix.EACCES) {
		// apparmor_restrict_unprivileged_userns is available since Ubuntu 23.10.
		// Enabled by default since Ubuntu 24.04.
		// https://github.com/containerd/nerdctl/issues/2847
		b, err := os.ReadFile("/proc/sys/kernel/apparmor_restrict_unprivileged_userns")
		if err == nil {
			s := strings.TrimSpace(string(b))
			i, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				logrus.WithError(err).Warnf("Failed to parse /proc/sys/kernel/apparmor_restrict_unprivileged_userns (%q)", s)
			} else if i == 1 {
				logrus.WithError(childStartErr).Warnf("This error might have happened because /proc/sys/kernel/apparmor_restrict_unprivileged_userns is set to 1")
				selfExe, err := os.Executable()
				if err != nil {
					selfExe = "/usr/local/bin/rootlesskit"
					logrus.WithError(err).Warnf("Failed to detect the path of the rootlesskit binary, assuming it to be %q", selfExe)
				}
				profileName := strings.ReplaceAll(strings.TrimPrefix(selfExe, "/"), "/", ".")
				const tmpl = `

########## BEGIN ##########
cat <<EOT | sudo tee "/etc/apparmor.d/%s"
# ref: https://ubuntu.com/blog/ubuntu-23-10-restricted-unprivileged-user-namespaces
abi <abi/4.0>,
include <tunables/global>

%s flags=(unconfined) {
  userns,

  # Site-specific additions and overrides. See local/README for details.
  include if exists <local/%s>
}
EOT
sudo systemctl restart apparmor.service
########## END ##########
`
				logrus.Warnf("Hint: try running the following commands:\n"+tmpl+"\n", profileName, selfExe, profileName)
			}
		}
	}
}
