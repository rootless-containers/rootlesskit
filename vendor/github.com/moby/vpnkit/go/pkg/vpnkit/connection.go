package vpnkit

import (
	"context"
)

// Client exposes and unexposes ports on vpnkit.
type Client interface {
	Expose(context.Context, *Port) error
	Unexpose(context.Context, *Port) error
	ListExposed(context.Context) ([]Port, error)
}
