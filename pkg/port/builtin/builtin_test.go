package builtin

import (
	"io/ioutil"
	"os"
	"testing"

	"github.com/rootless-containers/rootlesskit/pkg/port"
	"github.com/rootless-containers/rootlesskit/pkg/port/testsuite"
)

func TestMain(m *testing.M) {
	cf := func() port.ChildDriver {
		return NewChildDriver(os.Stderr)
	}
	testsuite.Main(m, cf)
}

func TestBuiltIn(t *testing.T) {
	tmpDir, err := ioutil.TempDir("", "test-builtin")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	d, err := NewParentDriver(os.Stderr, tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	pf := func() port.ParentDriver {
		return d
	}
	testsuite.Run(t, pf)
}
