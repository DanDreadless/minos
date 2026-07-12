//go:build !linux

package clients

// readIPv6Neighbors has no implementation off Linux (the Windows dev box is
// dev-only surface); IPv6 clients there simply stay MAC-untagged.
func readIPv6Neighbors() map[string]string { return nil }
