package fuseoverlayfs

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"

	"github.com/rootless-containers/rootlesskit/pkg/copyup"
)

// NewChildDriver requires tmpDir to be under /tmp .
// This restriction is for supporting --copy-up=/run .
func NewChildDriver(tmpDir string) (copyup.ChildDriver, error) {
	if !path.IsAbs(tmpDir) {
		return nil, errors.Errorf("tmpDir %q needs to be absolute", tmpDir)
	}
	if !strings.HasPrefix(tmpDir, "/tmp/") {
		return nil, errors.Errorf("tmpDir %q needs to be under /tmp", tmpDir)
	}
	fuseoverlayfsBinary, err := exec.LookPath("fuse-overlayfs")
	if err != nil {
		return nil, err
	}
	return &childDriver{
		tmpDir:              tmpDir,
		fuseoverlayfsBinary: fuseoverlayfsBinary,
	}, nil
}

type childDriver struct {
	tmpDir              string
	fuseoverlayfsBinary string
}

func (drv *childDriver) CopyUp(dirs []string) ([]string, error) {
	var copied []string
	for _, d := range dirs {
		d := filepath.Clean(d)
		if d == "/tmp" || d == "/" || strings.Contains(d, ",") {
			return copied, errors.Errorf("%s cannot be copied up", d)
		}

		tmp, err := ioutil.TempDir(drv.tmpDir, "")
		if err != nil {
			return copied, err
		}
		for _, base := range []string{"u", "w", "m"} {
			dir := filepath.Join(tmp, base)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return copied, err
			}
		}

		cmd := exec.Command(drv.fuseoverlayfsBinary,
			"-o",
			fmt.Sprintf("lowerdir=%s,upperdir=u,workdir=w", d),
			"none",
			"m",
		)
		cmd.Dir = tmp

		if out, err := cmd.CombinedOutput(); err != nil {
			return copied, errors.Wrapf(err, "fuse-overlayfs (%v) failed: %q",
				cmd.Args,
				string(out))
		}

		if err := unix.Mount(filepath.Join(tmp, "m"), d, "", uintptr(unix.MS_BIND), ""); err != nil {
			return nil, err
		}

		copied = append(copied, d)
	}
	return copied, nil
}
