package parent

import (
	"os"
	"os/user"
	"testing"

	"github.com/rootless-containers/rootlesskit/v3/pkg/parent/idtools"
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

func TestNewugidmapArgsFromSubIDRangesWithSelfRange(t *testing.T) {
	// The user's own ID is always mapped to 0, so a subid range that consists
	// of the own ID has to be excluded, otherwise newuidmap fails with EINVAL.
	u := &user.User{Uid: "1001", Gid: "1001"}
	subuidRanges := []idtools.SubIDRange{
		{Start: 1001, Length: 1},
		{Start: 165536, Length: 65536},
	}
	subgidRanges := []idtools.SubIDRange{
		{Start: 1001, Length: 1},
		{Start: 165536, Length: 65536},
	}
	newuidmapArgs, newgidmapArgs, err := newugidmapArgsFromSubIDRanges(u, subuidRanges, subgidRanges)
	assert.NilError(t, err)
	expectedU := []string{
		"0", u.Uid, "1", "1", "165536", "65536",
	}
	expectedG := []string{
		"0", u.Gid, "1", "1", "165536", "65536",
	}
	assert.DeepEqual(t, expectedU, newuidmapArgs)
	assert.DeepEqual(t, expectedG, newgidmapArgs)
}

func TestNewugidmapArgsFromSubIDRangesWithSelfInsideRange(t *testing.T) {
	// The own UID 1001 is in the middle of the range, the own GID 1001 is at
	// the end of the range. Both have to be excluded from the ranges.
	u := &user.User{Uid: "1001", Gid: "1001"}
	subuidRanges := []idtools.SubIDRange{
		{Start: 1000, Length: 10},
	}
	subgidRanges := []idtools.SubIDRange{
		{Start: 1000, Length: 2},
		{Start: 2000, Length: 3},
	}
	newuidmapArgs, newgidmapArgs, err := newugidmapArgsFromSubIDRanges(u, subuidRanges, subgidRanges)
	assert.NilError(t, err)
	expectedU := []string{
		"0", u.Uid, "1", "1", "1000", "1", "2", "1002", "8",
	}
	expectedG := []string{
		"0", u.Gid, "1", "1", "1000", "1", "2", "2000", "3",
	}
	assert.DeepEqual(t, expectedU, newuidmapArgs)
	assert.DeepEqual(t, expectedG, newgidmapArgs)
}
