package iputils

import (
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"net/netip"
)

func AddIPInt(ip net.IP, i int) (net.IP, error) {
	ip = ip.To4()
	if ip == nil {
		return nil, fmt.Errorf("expected IPv4 address, got %s", ip.String())
	}
	ui32 := binary.BigEndian.Uint32(ip)
	resInt64 := int64(ui32) + int64(i)
	if resInt64 > int64(math.MaxUint32) {
		return nil, fmt.Errorf("%s + %d overflows", ip.String(), i)
	}
	res := make(net.IP, 4)
	binary.BigEndian.PutUint32(res, uint32(resInt64))
	return res, nil
}

func AddIPInt6(ip net.IP, i int) (net.IP, error) {
	ip6 := ip.To16()
	if ip.To4() != nil || ip6 == nil {
		return nil, fmt.Errorf("expected IPv6 address, got %s", ip.String())
	}
	if i < 0 {
		return nil, fmt.Errorf("expected non-negative integer, got %d", i)
	}
	addr, ok := netip.AddrFromSlice(ip6)
	if !ok {
		return nil, fmt.Errorf("expected IPv6 address, got %s", ip.String())
	}
	for n := 0; n < i; n++ {
		addr = addr.Next()
		if !addr.IsValid() {
			return nil, fmt.Errorf("%s + %d overflows", ip.String(), i)
		}
	}
	return net.IP(addr.AsSlice()), nil
}
