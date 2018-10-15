package portutil

import (
	"regexp"
	"strconv"

	"github.com/pkg/errors"

	"github.com/rootless-containers/rootlesskit/pkg/port"
)

// ParsePortSpec parses a Docker-like representation of PortSpec.
// e.g. "127.0.0.1:8080:80/tcp"
func ParsePortSpec(s string) (*port.Spec, error) {
	r := regexp.MustCompile("^([0-9a-f\\.]+):([0-9]+):([0-9]+)/([a-z]+)$")
	g := r.FindStringSubmatch(s)
	if len(g) != 5 {
		return nil, errors.Errorf("unexpected PortSpec string: %q", s)
	}
	parentIP := g[1]
	parentPort, err := strconv.Atoi(g[2])
	if err != nil {
		return nil, errors.Wrapf(err, "unexpected ParentPort in PortSpec string: %q", s)
	}
	childPort, err := strconv.Atoi(g[3])
	if err != nil {
		return nil, errors.Wrapf(err, "unexpected ChildPort in PortSpec string: %q", s)
	}
	proto := g[4]
	// validation is up to the caller (as json.Unmarshal doesn't validate values)
	return &port.Spec{
		Proto:      proto,
		ParentIP:   parentIP,
		ParentPort: parentPort,
		ChildPort:  childPort,
	}, nil
}
