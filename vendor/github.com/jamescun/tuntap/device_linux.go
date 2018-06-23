// +build linux

package tuntap

import (
	"os"
	"strings"
	"syscall"
	"unsafe"
)

const (
	cIFF_TUN   = 0x0001
	cIFF_TAP   = 0x0002
	cIFF_NO_PI = 0x1000
)

type device struct {
	n string
	f *os.File
}

func (d *device) Name() string                { return d.n }
func (d *device) String() string              { return d.n }
func (d *device) Close() error                { return d.f.Close() }
func (d *device) Read(p []byte) (int, error)  { return d.f.Read(p) }
func (d *device) Write(p []byte) (int, error) { return d.f.Write(p) }

func newTUN(name string) (Interface, error) {
	file, err := os.OpenFile("/dev/net/tun", os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}

	iface, err := createTuntapInterface(file.Fd(), name, cIFF_TUN|cIFF_NO_PI)
	if err != nil {
		return nil, err
	}

	return &device{n: iface, f: file}, nil
}

func newTAP(name string) (Interface, error) {
	file, err := os.OpenFile("/dev/net/tun", os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}

	iface, err := createTuntapInterface(file.Fd(), name, cIFF_TAP|cIFF_NO_PI)
	if err != nil {
		return nil, err
	}

	return &device{n: iface, f: file}, nil
}

type tuntapInterface struct {
	Name  [0x10]byte
	Flags uint16
	pad   [0x28 - 0x10 - 2]byte
}

func createTuntapInterface(fd uintptr, name string, flags uint16) (string, error) {
	var req tuntapInterface
	req.Flags = flags
	copy(req.Name[:], name)

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(syscall.TUNSETIFF), uintptr(unsafe.Pointer(&req)))
	if errno != 0 {
		return "", errno
	}

	return strings.Trim(string(req.Name[:]), "\x00"), nil
}
