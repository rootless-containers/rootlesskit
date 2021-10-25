package child

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// generateEtcHosts makes sure the current hostname is resolved into
// 127.0.0.1 or ::1, not into the host eth0 IP address.
//
// Note that /etc/hosts is not used by nslookup/dig. (Use `getent ahostsv4` instead.)
func generateEtcHosts() ([]byte, error) {
	etcHosts, err := os.ReadFile("/etc/hosts")
	if err != nil {
		return nil, err
	}
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	// FIXME: no need to add the entry if already added
	s := fmt.Sprintf("%s\n127.0.0.1 %s\n::1 %s\n",
		string(etcHosts), hostname, hostname)
	return []byte(s), nil
}

// writeEtcHosts is akin to writeResolvConf
// TODO: dedupe
func writeEtcHosts() error {
	newEtcHosts, err := generateEtcHosts()
	if err != nil {
		return err
	}
	// remove copied-up link
	_ = os.Remove("/etc/hosts")
	if err := os.WriteFile("/etc/hosts", newEtcHosts, 0644); err != nil {
		return fmt.Errorf("writing /etc/hosts: %w", err)
	}
	return nil
}

// mountEtcHosts is akin to mountResolvConf
// TODO: dedupe
func mountEtcHosts(tempDir string) error {
	newEtcHosts, err := generateEtcHosts()
	if err != nil {
		return err
	}
	myEtcHosts := filepath.Join(tempDir, "hosts")
	if err := os.WriteFile(myEtcHosts, newEtcHosts, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", myEtcHosts, err)
	}

	if err := unix.Mount(myEtcHosts, "/etc/hosts", "", uintptr(unix.MS_BIND), ""); err != nil {
		return fmt.Errorf("failed to create bind mount /etc/hosts for %s: %w", myEtcHosts, err)
	}
	return nil
}
