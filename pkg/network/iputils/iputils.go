package iputils

import (
	"encoding/binary"
	"math"
	"net"

	"github.com/pkg/errors"
)

func AddIPInt(ip net.IP, i int) (net.IP, error) {
	ip = ip.To4()
	if ip == nil {
		return nil, errors.Errorf("expected IPv4 address, got %s", ip.String())
	}
	ui32 := binary.BigEndian.Uint32(ip)
	resInt := int(ui32) + i
	if resInt > math.MaxUint32 {
		return nil, errors.Errorf("%s + %d overflows", ip.String(), i)
	}
	res := make(net.IP, 4)
	binary.BigEndian.PutUint32(res, uint32(resInt))
	return res, nil
}
