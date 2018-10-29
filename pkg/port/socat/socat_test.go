package socat

import (
	"testing"

	"github.com/rootless-containers/rootlesskit/pkg/port"
	"github.com/rootless-containers/rootlesskit/pkg/port/testsuite"
)

func TestSocat(t *testing.T) {
	df := func() port.Driver {
		d, err := New(testsuite.TLogWriter(t, "socat.Driver"))
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	testsuite.Run(t, df)
}
