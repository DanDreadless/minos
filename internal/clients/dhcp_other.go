//go:build !linux

package clients

import "context"

// listenDHCP has no implementation off Linux (the Windows dev box is
// dev-only surface); lease-time device names come from the other sources.
func (r *Registry) listenDHCP(context.Context) {}
