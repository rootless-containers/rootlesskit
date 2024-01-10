package child

import (
	"fmt"
	"os"
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
