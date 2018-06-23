// +build darwin

package tuntap

import (
	"os"
	"strconv"
	"syscall"
)

// maximum devices supported by driver
const maxDevices = 16

type device struct {
	n string
	f *os.File
}

func (d *device) Name() string   { return d.n }
func (d *device) String() string { return d.n }
func (d *device) Close() error   { return d.f.Close() }

func (d *device) Read(p []byte) (n int, err error) {
	n, err = d.f.Read(p)
	if isNotReady(err) {
		err = ErrNotReady
	}

	return
}

func (d *device) Write(p []byte) (n int, err error) {
	n, err = d.f.Write(p)
	if isNotReady(err) {
		err = ErrNotReady
	}

	return
}

// return true if read error is result of device not being ready
func isNotReady(err error) bool {
	if perr, ok := err.(*os.PathError); ok {
		if code, ok := perr.Err.(syscall.Errno); ok {
			if code == 0x05 {
				return true
			}
		}
	}

	return false
}

// return true if file error is result of device already being used
func isBusy(err error) bool {
	if perr, ok := err.(*os.PathError); ok {
		if code, ok := perr.Err.(syscall.Errno); ok {
			if code == 0x10 || code == 0x11 { // device busy || exclusive lock
				return true
			}
		}
	}

	return false
}

func newTUN(name string) (Interface, error) {
	if len(name) == 0 {
		// dynamic device
		for i := 0; i < maxDevices; i++ {
			iface, err := newDevice("/dev/tun" + strconv.Itoa(i))
			if err == ErrBusy {
				// device already used
				continue
			} else if err != nil {
				// other error
				return nil, err
			}

			return iface, nil
		}

		return nil, ErrExhausted
	}

	// static device
	return newDevice(name)
}

func newTAP(name string) (Interface, error) {
	if len(name) == 0 {
		// dynamic device
		for i := 0; i < maxDevices; i++ {
			iface, err := newDevice("/dev/tap" + strconv.Itoa(i))
			if err == ErrBusy {
				// device already used
				continue
			} else if err != nil {
				// other error
				return nil, err
			}

			return iface, nil
		}

		return nil, ErrExhausted
	}

	// static device
	return newDevice(name)
}

func newDevice(name string) (Interface, error) {
	file, err := os.OpenFile(name, os.O_EXCL|os.O_RDWR, 0)
	if isBusy(err) {
		return nil, ErrBusy
	} else if err != nil {
		return nil, err
	}

	return &device{n: name, f: file}, nil
}
