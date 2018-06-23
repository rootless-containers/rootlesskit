package tuntap

import (
	"errors"
)

var (
	ErrBusy        = errors.New("device is already in use")
	ErrNotReady    = errors.New("device is not ready")
	ErrExhausted   = errors.New("no devices are available")
	ErrUnsupported = errors.New("device is unsupported on this platform")
)

// Interface represents a TUN/TAP network interface
type Interface interface {
	// return name of TUN/TAP interface
	Name() string

	// implement io.Reader interface, read bytes into p from TUN/TAP interface
	Read(p []byte) (n int, err error)

	// implement io.Writer interface, write bytes from p to TUN/TAP interface
	Write(p []byte) (n int, err error)

	// implement io.Closer interface, must be called when done with TUN/TAP interface
	Close() error

	// return string representation of TUN/TAP interface
	String() string
}

// return a TUN interface. depending on platform, the device may not be ready
// for use yet; a caller must poll the Ready() method before use. additionally
// the caller is responsible for calling Close() to terminate the device.
func Tun(name string) (Interface, error) {
	// call platform specific device creation
	return newTUN(name)
}

// return a TAP interface. depending on platform, the device may not be ready
// for use yet; a caller must poll the Ready() method before use. additionally
// the caller is responsible for calling Close() to terminate the device.
func Tap(name string) (Interface, error) {
	// call platform specific device creation
	return newTAP(name)
}
