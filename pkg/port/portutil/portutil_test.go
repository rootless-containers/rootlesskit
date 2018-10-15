package portutil

import (
	"reflect"
	"testing"

	"github.com/rootless-containers/rootlesskit/pkg/port"
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
	}
	for _, tc := range testCases {
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
	}
}
