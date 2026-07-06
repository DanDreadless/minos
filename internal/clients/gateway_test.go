package clients

import (
	"net"
	"strings"
	"testing"
)

func TestParseProcNetRoute(t *testing.T) {
	const header = "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n"
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "typical default route",
			// Gateway 0101A8C0 = little-endian 192.168.1.1.
			body: "eth0\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" +
				"eth0\t0001A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n",
			want: "192.168.1.1",
		},
		{
			name: "default route not first",
			body: "eth0\t0001A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n" +
				"eth0\t00000000\tFE01A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n",
			want: "192.168.1.254",
		},
		{
			name: "no default route",
			body: "eth0\t0001A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n",
			want: "",
		},
		{
			name: "default route with zero gateway is skipped",
			body: "eth0\t00000000\t00000000\t0001\t0\t0\t100\t00000000\t0\t0\t0\n",
			want: "",
		},
		{
			name: "junk lines are tolerated",
			body: "garbage\n\neth0\t00000000\t0A00000A\t0003\t0\t0\t0\t0\t0\t0\t0\n",
			want: "10.0.0.10",
		},
		{
			name: "empty",
			body: "",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseProcNetRoute(strings.NewReader(header + tt.body))
			if got != tt.want {
				t.Errorf("parseProcNetRoute = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReverseResolvers(t *testing.T) {
	// With a gateway, the gateway resolver is tried before the system one.
	withGW := reverseResolvers("192.168.1.1")
	if len(withGW) != 2 {
		t.Fatalf("with gateway: got %d resolvers, want 2", len(withGW))
	}
	if withGW[0] == net.DefaultResolver {
		t.Error("with gateway: system resolver should not be first")
	}
	if withGW[1] != net.DefaultResolver {
		t.Error("with gateway: system resolver should be the fallback")
	}

	// Without a gateway, only the system resolver is used.
	noGW := reverseResolvers("")
	if len(noGW) != 1 || noGW[0] != net.DefaultResolver {
		t.Fatalf("no gateway: got %d resolvers, want [DefaultResolver]", len(noGW))
	}
}
