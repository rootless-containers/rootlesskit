package builtin

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/rootless-containers/rootlesskit/v3/pkg/port"
	"github.com/rootless-containers/rootlesskit/v3/pkg/port/testsuite"
)

func TestMain(m *testing.M) {
	cf := func() port.ChildDriver {
		return NewChildDriver(os.Stderr)
	}
	testsuite.Main(m, cf)
}

func TestBuiltIn(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test-builtin")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	d, err := NewParentDriver(os.Stderr, tmpDir, true, "auto")
	if err != nil {
		t.Fatal(err)
	}
	pf := func() port.ParentDriver {
		return d
	}
	testsuite.Run(t, pf)
	testsuite.RunTCPTransparent(t, pf)
	testsuite.RunUDPTransparent(t, pf)
}

// TestSourceIPTransparentBackend exercises an explicit
// --source-ip-transparent-backend selection end to end, and checks that the
// backend actually resolved by the child (via the init handshake) is
// reported back via PortDriverInfo.Extra.
func TestSourceIPTransparentBackend(t *testing.T) {
	for _, backend := range []string{"nft", "iptables"} {
		t.Run(backend, func(t *testing.T) {
			if backend == "nft" {
				ensureNFT(t)
			}
			tmpDir, err := os.MkdirTemp("", "test-builtin-backend")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(tmpDir)
			d, err := NewParentDriver(os.Stderr, tmpDir, true, backend)
			if err != nil {
				t.Fatal(err)
			}
			// RunTCPTransparent drives RunParentDriver, which performs the
			// init handshake that eagerly resolves the backend. Info() only
			// reflects the resolved value once that handshake has happened.
			testsuite.RunTCPTransparent(t, func() port.ParentDriver { return d })
			info, err := d.Info(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if info.Extra == nil {
				t.Fatal("expected PortDriverInfo.Extra to be set")
			}
			if got := info.Extra["sourceIPTransparentBackend"]; got != backend {
				t.Fatalf("expected PortDriverInfo.Extra[sourceIPTransparentBackend]=%q, got %q", backend, got)
			}
		})
	}
}

func ensureNFT(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skipf("nft not found: %v", err)
	}
}
