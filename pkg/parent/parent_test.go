package parent

import (
	"os"
	"os/user"
	"testing"

	"github.com/rootless-containers/rootlesskit/v3/pkg/parent/idtools"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
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

func TestWithoutSelfID(t *testing.T) {
	ranges := []idtools.SubIDRange{
		{Start: 1001, Length: 1},
		{Start: 2000, Length: 10},
		{Start: 3000, Length: 5},
	}
	res, removed := withoutSelfID(ranges, 2005)
	expectedRes := []idtools.SubIDRange{
		{Start: 1001, Length: 1},
		{Start: 2000, Length: 5},
		{Start: 2006, Length: 4},
		{Start: 3000, Length: 5},
	}
	expectedRemoved := []idtools.SubIDRange{
		{Start: 2000, Length: 10},
	}
	assert.DeepEqual(t, expectedRes, res)
	assert.DeepEqual(t, expectedRemoved, removed)

	// An ID that is not in any range keeps the ranges unmodified.
	res, removed = withoutSelfID(ranges, 4000)
	assert.DeepEqual(t, ranges, res)
	assert.Assert(t, removed == nil)
}

func TestNewugidmapArgsFromSubIDRangesWarnsAboutSelfID(t *testing.T) {
	// The warning tells the admin which range of /etc/subuid and /etc/subgid
	// is misconfigured. It is printed once per range, not once per ID.
	hook := test.NewGlobal()
	defer hook.Reset()
	u := &user.User{Uid: "1001", Gid: "1001"}
	subuidRanges := []idtools.SubIDRange{
		{Start: 1000, Length: 10},
		{Start: 165536, Length: 65536},
	}
	subgidRanges := []idtools.SubIDRange{
		{Start: 1001, Length: 1},
	}
	_, _, err := newugidmapArgsFromSubIDRanges(u, subuidRanges, subgidRanges)
	assert.NilError(t, err)
	entries := hook.AllEntries()
	assert.Equal(t, 2, len(entries))
	assert.Equal(t, logrus.WarnLevel, entries[0].Level)
	assert.Equal(t, "/etc/subuid: the range 1000:10 contains the own UID 1001, which is already mapped to UID 0 in the user namespace. RootlessKit ignores the UID 1001 in this range. Remove the own UID from /etc/subuid.", entries[0].Message)
	assert.Equal(t, logrus.WarnLevel, entries[1].Level)
	assert.Equal(t, "/etc/subgid: the range 1001:1 contains the own GID 1001, which is already mapped to GID 0 in the user namespace. RootlessKit ignores the GID 1001 in this range. Remove the own GID from /etc/subgid.", entries[1].Message)
}
