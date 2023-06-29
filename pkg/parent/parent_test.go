package parent

import (
	"os"
	"os/user"
	"testing"

	"github.com/rootless-containers/rootlesskit/v2/pkg/parent/idtools"
	"golang.org/x/sys/unix"
	"gotest.tools/v3/assert"
)

func TestBSDLockFileCreated(t *testing.T) {

	tmpDir, err := os.MkdirTemp("", "rootlesskit")
	if err != nil {
		t.Fatalf("expected no error, got %q", err)
	}

	err = createCleanupLock(tmpDir)
	if err != nil {
		t.Fatalf("expected no error, got %q", err)
	}

	stateDir, _ := os.Open(tmpDir)
	err = unix.Flock(int(stateDir.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		t.Fatal("expected that there was an error because of existing LOCK_SH")
	}
}

func TestNewugidmapArgsFromSubIDRanges(t *testing.T) {
	subuidRanges := []idtools.SubIDRange{
		{Start: 100000, Length: 65536},
		{Start: 200000, Length: 65536},
	}
	subgidRanges := []idtools.SubIDRange{
		{Start: 100000, Length: 65536},
		{Start: 200000, Length: 65536},
	}
	u, err := user.Current()
	assert.NilError(t, err)
	newuidmapArgs, newgidmapArgs, err := newugidmapArgsFromSubIDRanges(u, subuidRanges, subgidRanges)
	assert.NilError(t, err)
	expectedU := []string{
		"0", u.Uid, "1", "1", "100000", "65536", "65537", "200000", "65536",
	}
	expectedG := []string{
		"0", u.Gid, "1", "1", "100000", "65536", "65537", "200000", "65536",
	}
	assert.DeepEqual(t, expectedU, newuidmapArgs)
	assert.DeepEqual(t, expectedG, newgidmapArgs)
}
