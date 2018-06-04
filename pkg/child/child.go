package child

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"

	"github.com/pkg/errors"
)

func waitForParentSync(pipeFDStr string, magicPacket []byte) error {
	pipeFD, err := strconv.Atoi(pipeFDStr)
	if err != nil {
		return errors.Wrapf(err, "unexpected fd value: %s", pipeFDStr)
	}
	pipeR := os.NewFile(uintptr(pipeFD), "")
	buf := make([]byte, len(magicPacket))
	if _, err := pipeR.Read(buf); err != nil {
		return errors.Wrapf(err, "failed to read fd %d", pipeFD)
	}
	if bytes.Compare(magicPacket, buf) != 0 {
		return errors.Errorf("expected magic packet %v, got %v", magicPacket, buf)
	}
	return pipeR.Close()
}

func createCmd(targetCmd []string) (*exec.Cmd, error) {
	var args []string
	if len(targetCmd) > 1 {
		args = targetCmd[1:]
	}
	cmd := exec.Command(targetCmd[0], args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	return cmd, nil
}

func Child(pipeFDEnvKey string, magicPacket []byte, targetCmd []string) error {
	pipeFDStr := os.Getenv(pipeFDEnvKey)
	if pipeFDStr == "" {
		return errors.Errorf("%s is not set", pipeFDEnvKey)
	}
	os.Unsetenv(pipeFDEnvKey)
	if err := waitForParentSync(pipeFDStr, magicPacket); err != nil {
		return err
	}
	cmd, err := createCmd(targetCmd)
	if err != nil {
		return err
	}
	return cmd.Run()
}
