package clients

// defaultGateway is unimplemented on Windows, a dev-only target. Reverse
// enrichment then falls back to the system resolver, which on a dev box is
// not Minos itself and answers PTR fine. The deploy targets are Linux, where
// gateway_linux.go reads /proc/net/route.
func defaultGateway() string { return "" }
