// +build !linux,!darwin

package tuntap

func newTUN(name string) (Interface, error) {
	return nil, ErrUnsupported
}

func newTAP(name string) (Interface, error) {
	return nil, ErrUnsupported
}
