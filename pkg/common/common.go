package common

type NetworkMode int

const (
	HostNetwork NetworkMode = iota
	VDEPlugSlirp
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
