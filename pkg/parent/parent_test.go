package parent

import (
	"io/ioutil"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestBSDLockFileCreated(t *testing.T) {

	tmpDir, err := ioutil.TempDir("", "rootlesskit")
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
