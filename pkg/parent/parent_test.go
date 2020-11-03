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

	err = createCleanupLock(tmpDir, true)
	if err != nil {
		t.Fatalf("expected no error, got %q", err)
	}

	stateDir, _ := os.Open(tmpDir)
	err = unix.Flock(int(stateDir.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		t.Fatal("expected that ther was an error because of existing LOCK_SH")
	}
}
func TestBSDLockFileNotCreated(t *testing.T) {

	tmpDir, err := ioutil.TempDir("", "rootlesskit")
	if err != nil {
		t.Fatalf("expected no error, got %q", err)
	}

	err = createCleanupLock(tmpDir, false)
	if err != nil {
		t.Fatalf("expected no error, got %q", err)
	}

	//validate that no lock was written by set a lock on dir manually
	stateDir, _ := os.Open(tmpDir)
	err = unix.Flock(int(stateDir.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err != nil {
		t.Fatalf("expected no error, got %q", err)
	}
}
