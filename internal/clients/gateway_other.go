//go:build !linux && !windows

package clients

// defaultGateway has no implementation on this platform; reverse enrichment
// falls back to the system resolver.
func defaultGateway() string { return "" }
