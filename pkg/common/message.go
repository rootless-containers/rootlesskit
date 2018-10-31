package common

// Message is sent from the parent to the child
// as JSON, with uint32le length header.
type Message struct {
	// StateDir cannot be empty
	StateDir string
	Network  NetworkMessage
}

// NetworkMessage is empty for HostNetwork.
type NetworkMessage struct {
	IP      string
	Netmask int
	Gateway string
	DNS     string
	MTU     int
	// Opaque strings are specific to driver
	Opaque map[string]string
}
