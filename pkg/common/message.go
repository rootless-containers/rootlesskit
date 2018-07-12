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
	// For vdeplug_slirp and slirp4netns
	PreconfiguredTap string
	// VPNKit settings
	VPNKitMAC    string
	VPNKitSocket string
	VPNKitUUID   string
	// CopyUp settings
	CopyUpMode
	CopyUpDirs []string
}
