package common

// Message is sent from the parent to the child
// as JSON, with uint32le length header.
type Message struct {
	NetworkMode
	Tap     string
	IP      string
	Netmask int
	Gateway string
	DNS     string
}
