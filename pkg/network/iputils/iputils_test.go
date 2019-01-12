package iputils

import (
	"net"
	"testing"
)

func TestAddIPInt(t *testing.T) {
	type testCase struct {
		s        string
		i        int
		expected string
	}
	testCases := []testCase{
		{
			"10.0.2.0",
			100,
			"10.0.2.100",
		},
		{
			"255.255.255.100",
			155,
			"255.255.255.255",
		},
		{
			"255.255.255.100",
			156,
			"",
		},
	}
	for i, tc := range testCases {
		ip := net.ParseIP(tc.s)
		if ip == nil {
			t.Fatalf("invalid IP: %q", tc.s)
		}
		gotIP, err := AddIPInt(ip, tc.i)
		if tc.expected == "" {
			if err == nil {
				t.Fatalf("#%d: expected error, got no error", i)
			}
		} else {
			if err != nil {
				t.Fatalf("#%d: expected no error, got %q", i, err)
			}
			got := gotIP.String()
			if got != tc.expected {
				t.Fatalf("#%d: expected %q, got %q", i, tc.expected, got)
			}
		}
	}
}
