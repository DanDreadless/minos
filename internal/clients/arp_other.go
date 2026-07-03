//go:build !linux && !windows

package clients

// readARPTable has no implementation on this platform; devices simply show
// without MAC addresses.
func readARPTable() map[string]string { return nil }
