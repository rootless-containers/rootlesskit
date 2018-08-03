package common

// Message is sent from the parent to the child
// as JSON, with uint32le length header.
type Message struct {
	// StateDir cannot be empty
	StateDir string
	// Network settings
	NetworkMode
	IP      string
	Netmask int
	Gateway string
	DNS     string
	MTU     int
	// For vdeplug_slirp and slirp4netns.
	// "preconfigured" just means tap is created and "up".
	// IP stuff and MTU are not configured by the parent here,
	// and they are up to the child.
	PreconfiguredTap string
	// VPNKit settings
	VPNKitMAC    string
	VPNKitSocket string
	VPNKitUUID   string
	// CopyUp settings
	CopyUpMode
	CopyUpDirs []string
}
