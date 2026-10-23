package dnsproxy

import (
	"strconv"
	"testing"

	"github.com/miekg/dns"
)

func TestDDRAdvertisesEncryptedListeners(t *testing.T) {
	srv := startEncryptedProxy(t)
	resp := query(t, srv.UDPAddr().String(), ddrQName, dns.TypeSVCB)
	if !resp.Authoritative || len(resp.Answer) != 2 {
		t.Fatalf("answer = %v", resp.Answer)
	}
	dot, doh := resp.Answer[0].(*dns.SVCB), resp.Answer[1].(*dns.SVCB)
	if dot.Target != "localhost." || dot.Priority != 1 || doh.Priority != 2 {
		t.Errorf("targets/priorities: %v / %v", dot, doh)
	}
	want := map[string]bool{`alpn="dot"`: false, `port="` + strconv.Itoa(int(portOf(srv.dotAddr))) + `"`: false}
	for _, kv := range dot.Value {
		want[kv.Key().String()+`="`+kv.String()+`"`] = true
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("DoT record lacks %s: %v", k, dot.Value)
		}
	}
	hasPath := false
	for _, kv := range doh.Value {
		if p, ok := kv.(*dns.SVCBDoHPath); ok && p.Template == "/dns-query{?dns}" {
			hasPath = true
		}
	}
	if !hasPath {
		t.Errorf("DoH record lacks dohpath: %v", doh.Value)
	}
}

// Without encrypted listeners resolver.arpa is still answered here — empty —
// never forwarded, so an upstream cannot hand devices its own endpoint.
func TestDDRNotForwardedWithoutListeners(t *testing.T) {
	srv, _ := startProxy(t, "zero_ip")
	resp := query(t, srv.UDPAddr().String(), ddrQName, dns.TypeSVCB)
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 0 || !resp.Authoritative {
		t.Errorf("got rcode %s, answer %v", dns.RcodeToString[resp.Rcode], resp.Answer)
	}
	for _, u := range srv.UpstreamStats() {
		if u.Requests > 0 {
			t.Errorf("resolver.arpa reached upstream %s", u.Name)
		}
	}
}
