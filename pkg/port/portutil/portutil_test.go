package portutil

import (
	"reflect"
	"testing"

	"github.com/rootless-containers/rootlesskit/pkg/port"
	"github.com/stretchr/testify/assert"
)

func TestParsePortSpec(t *testing.T) {
	type testCase struct {
		s string
		// nil for invalid string
		expected *port.Spec
	}
	testCases := []testCase{
		{
			s: "127.0.0.1:8080:80/tcp",
			expected: &port.Spec{
				Proto:      "tcp",
				ParentIP:   "127.0.0.1",
				ParentPort: 8080,
				ChildPort:  80,
			},
		},
		{
			s: "127.0.0.1:8080:10.0.2.100:80/tcp",
			expected: &port.Spec{
				Proto:      "tcp",
				ParentIP:   "127.0.0.1",
				ParentPort: 8080,
				ChildIP:    "10.0.2.100",
				ChildPort:  80,
			},
		},
		{
			s: "bad",
		},
		{
			s: "127.0.0.1:8080:80/tcp,127.0.0.1:4040:40/tcp",
			// one entry per one string
		},
		{
			s: "8080",
			// future version may support short formats like this
		},
		{
			s: "[::1]:8080:80/tcp",
			expected: &port.Spec{
				Proto:      "tcp",
				ParentIP:   "::1",
				ParentPort: 8080,
				ChildPort:  80,
			},
		},
		{
			s: "[::1]:8080:[::2]:80/udp",
			expected: &port.Spec{
				Proto:      "udp",
				ParentIP:   "::1",
				ParentPort: 8080,
				ChildIP:    "::2",
				ChildPort:  80,
			},
		},
	}
	for _, tc := range testCases {
		tc := tc
		t.Run(tc.s, func(t *testing.T) {
			got, err := ParsePortSpec(tc.s)
			if tc.expected == nil {
				if err == nil {
					t.Fatalf("error is expected for %q", tc.s)
				}
			} else {
				if err != nil {
					t.Fatalf("got error for %q: %v", tc.s, err)
				}
				if !reflect.DeepEqual(got, tc.expected) {
					t.Fatalf("expected %+v, got %+v", tc.expected, got)
				}
			}
		})
	}
}

func TestValidatePortSpec(t *testing.T) {
	existingPorts := make(map[int]*port.Status)

	// bind to all host IPs
	existingPorts[1] = &port.Status{
		ID: 1,
		Spec: port.Spec{
			Proto:      "tcp",
			ParentIP:   "",
			ParentPort: 80,
			ChildPort:  80,
		},
	}
	// bind to only host IP 10.10.10.10
	existingPorts[2] = &port.Status{
		ID: 2,
		Spec: port.Spec{
			Proto:      "tcp",
			ParentIP:   "10.10.10.10",
			ParentPort: 8080,
			ChildPort:  8080,
		},
	}
	// avoid typing the spec over and over for small changes
	spec := port.Spec{
		Proto:      "tcp",
		ParentIP:   "127.0.0.1",
		ParentPort: 1001,
		ChildPort:  1001,
	}

	// proto must be supplied and must equal "udp" or "tcp"
	invalidProtos := []string{"", "NaN", "TCP"}
	validProtos := []string{"udp", "tcp"}
	for _, p := range invalidProtos {
		s := spec
		s.Proto = p
		err := ValidatePortSpec(s, existingPorts)
		assert.Error(t, err)
	}
	for _, p := range validProtos {
		s := spec
		s.Proto = p
		err := ValidatePortSpec(s, existingPorts)
		assert.NoError(t, err)

	}

	invalidPorts := []int{-200, 0, 1000000}
	validPorts := []int{20, 500, 1337, 65000}

	// 0 < parentPort <= 65535
	for _, p := range invalidPorts {
		s := spec
		s.ParentPort = p
		err := ValidatePortSpec(s, existingPorts)
		assert.Error(t, err)
	}
	for _, p := range validPorts {
		s := spec
		s.ParentPort = p
		err := ValidatePortSpec(s, existingPorts)
		assert.NoError(t, err)
	}

	// 0 < childPort <= 65535
	for _, p := range invalidPorts {
		s := spec
		s.ChildPort = p
		err := ValidatePortSpec(s, existingPorts)
		assert.Error(t, err, "invalid ChildPort")
	}
	for _, p := range validPorts {
		s := spec
		s.ChildPort = p
		err := ValidatePortSpec(s, existingPorts)
		assert.NoError(t, err)
	}

	// ChildPorts can overlap so long as parent port/IPs don't
	// existing ports include tcp 10.10.10.10:8080, tcp *:80, no udp

	// udp doesn't conflict with tcp
	s := port.Spec{Proto: "udp", ParentPort: 80, ChildPort: 80}
	assert.NoError(t, ValidatePortSpec(s, existingPorts))

	// same parent, same child, different IP has no conflict
	s = port.Spec{Proto: "tcp", ParentIP: "10.10.10.11", ParentPort: 8080, ChildPort: 8080}
	assert.NoError(t, ValidatePortSpec(s, existingPorts))

	// same IP different parentPort, same child port has no conflict
	s = port.Spec{Proto: "tcp", ParentIP: "10.10.10.10", ParentPort: 8081, ChildPort: 8080}
	assert.NoError(t, ValidatePortSpec(s, existingPorts))

	// Same parent IP and Port should conflict, even if child port different
	// conflict with ID 1:
	s = port.Spec{Proto: "tcp", ParentPort: 80, ChildPort: 90}
	err := ValidatePortSpec(s, existingPorts)
	assert.EqualError(t, err, "conflict with ID 1")

	// conflict with ID 2
	s = port.Spec{Proto: "tcp", ParentIP: "10.10.10.10", ParentPort: 8080, ChildPort: 8080}
	err = ValidatePortSpec(s, existingPorts)
	assert.EqualError(t, err, "conflict with ID 2")
}
