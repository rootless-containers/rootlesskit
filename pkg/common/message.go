package common

// Message is sent from the parent to the child
// as JSON, with uint32le length header.
type Message struct {
	NetworkMode
	IP           string
	Netmask      int
	Gateway      string
	DNS          string
	VDEPlugTap   string
	VPNKitMAC    string
	VPNKitSocket string
	VPNKitUUID   string
}
