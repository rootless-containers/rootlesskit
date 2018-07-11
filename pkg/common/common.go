package common

type NetworkMode int

const (
	HostNetwork NetworkMode = iota
	VDEPlugSlirp
	VPNKit
	Slirp4NetNS
)

type CopyUpMode int

const (
	TmpfsWithSymlinkCopyUp CopyUpMode = iota
	// TODO: add "naive copy", overlayfs, bind-mount
)

func Seq(fns []func() error) func() error {
	return func() error {
		for _, fn := range fns {
			if err := fn(); err != nil {
				return err
			}
		}
		return nil
	}
}
