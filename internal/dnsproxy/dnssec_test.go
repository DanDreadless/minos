package dnsproxy

// End-to-end DNSSEC wiring tests: a stub upstream serves a miniature
// signed hierarchy (root -> com -> www.com) and the proxy validates
// through the real forward path, cache, and docket.

import (
	"context"
	"crypto"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"

	"minos/internal/clients"
	"minos/internal/config"
	"minos/internal/filter"
	"minos/internal/querylog"
)

type miniZone struct {
	name string
	key  *dns.DNSKEY
	priv crypto.Signer
}

func newMiniZone(t *testing.T, name string) *miniZone {
	t.Helper()
	key := &dns.DNSKEY{
		Hdr:       dns.RR_Header{Name: name, Rrtype: dns.TypeDNSKEY, Class: dns.ClassINET, Ttl: 3600},
		Flags:     257,
		Protocol:  3,
		Algorithm: dns.ECDSAP256SHA256,
	}
	priv, err := key.Generate(256)
	if err != nil {
		t.Fatal(err)
	}
	return &miniZone{name: name, key: key, priv: priv.(crypto.Signer)}
}

func (z *miniZone) sign(t *testing.T, rrset []dns.RR) *dns.RRSIG {
	t.Helper()
	sig := &dns.RRSIG{
		Hdr: dns.RR_Header{
			Name: rrset[0].Header().Name, Rrtype: dns.TypeRRSIG,
			Class: dns.ClassINET, Ttl: rrset[0].Header().Ttl,
		},
		TypeCovered: rrset[0].Header().Rrtype,
		Algorithm:   z.key.Algorithm,
		OrigTtl:     rrset[0].Header().Ttl,
		Expiration:  uint32(time.Now().Add(24 * time.Hour).Unix()),
		Inception:   uint32(time.Now().Add(-time.Hour).Unix()),
		KeyTag:      z.key.KeyTag(),
		SignerName:  z.name,
	}
	if err := sig.Sign(z.priv, rrset); err != nil {
		t.Fatal(err)
	}
	return sig
}

// signedStub runs an upstream serving the signed chain for www.com (or an
// entirely unsigned answer when signed=false). tamper corrupts the A
// record after signing. sawDO records the DO bit on the last www.com/A
// query the stub received.
func signedStub(t *testing.T, signed, tamper bool) (addr string, anchors []*dns.DS, sawDO *atomic.Bool) {
	t.Helper()
	sawDO = new(atomic.Bool)
	a := &dns.A{
		Hdr: dns.RR_Header{Name: "www.com.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
		A:   net.IPv4(203, 0, 113, 80),
	}
	answers := map[string][]dns.RR{}
	if signed {
		root := newMiniZone(t, ".")
		com := newMiniZone(t, "com.")
		rootKeys := []dns.RR{root.key}
		comKeys := []dns.RR{com.key}
		comDS := com.key.ToDS(dns.SHA256)
		comDS.Hdr.Ttl = 3600
		dsSet := []dns.RR{comDS}
		answers[".|DNSKEY"] = append(rootKeys, root.sign(t, rootKeys))
		answers["com.|DS"] = append(dsSet, root.sign(t, dsSet))
		answers["com.|DNSKEY"] = append(comKeys, com.sign(t, comKeys))
		answers["www.com.|A"] = []dns.RR{a, com.sign(t, []dns.RR{a})}
		anchor := root.key.ToDS(dns.SHA256)
		anchors = []*dns.DS{anchor}
	} else {
		answers["www.com.|A"] = []dns.RR{a}
	}
	if tamper {
		a.A = net.IPv4(198, 51, 100, 66) // forged after signing
	}

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &dns.Server{
		PacketConn: pc,
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
			reply := new(dns.Msg)
			reply.SetReply(req)
			if len(req.Question) == 1 {
				q := req.Question[0]
				key := strings.ToLower(q.Name) + "|" + dns.TypeToString[q.Qtype]
				if key == "www.com.|A" {
					opt := req.IsEdns0()
					sawDO.Store(opt != nil && opt.Do())
				}
				reply.Answer = answers[key]
			}
			_ = w.WriteMsg(reply)
		}),
	}
	go func() { _ = srv.ActivateAndServe() }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return pc.LocalAddr().String(), anchors, sawDO
}

// dnssecProxy starts a judged proxy in the given dns.dnssec mode against
// the signed stub.
func dnssecProxy(t *testing.T, mode string, signed, tamper bool) (*Server, *querylog.Log, *atomic.Bool) {
	t.Helper()
	upstream, anchors, sawDO := signedStub(t, signed, tamper)

	engine := filter.NewEngine()
	engine.Swap(filter.NewBuilder().Build())
	qlog, err := querylog.Open(querylog.Options{RingSize: 100, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = qlog.Close() })

	cfg := config.Default()
	cfg.DNS.Listen = "127.0.0.1:0"
	cfg.DNS.Upstreams = []config.Upstream{{Address: upstream, Protocol: "udp"}}
	cfg.DNS.DNSSEC = mode
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	reg := clients.NewRegistry()
	reg.ApplyConfig(cfg)
	srv, err := New(cfg, engine, qlog, reg)
	if err != nil {
		t.Fatal(err)
	}
	if anchors != nil {
		srv.SetTrustAnchors(anchors)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return srv, qlog, sawDO
}

func queryDO(t *testing.T, addr, qname string, qtype uint16) *dns.Msg {
	t.Helper()
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(qname), qtype)
	m.SetEdns0(1232, true)
	c := &dns.Client{Timeout: 3 * time.Second}
	resp, _, err := c.Exchange(m, addr)
	if err != nil {
		t.Fatalf("query %s: %v", qname, err)
	}
	return resp
}

func hasRRSIG(m *dns.Msg) bool {
	for _, rr := range m.Answer {
		if rr.Header().Rrtype == dns.TypeRRSIG {
			return true
		}
	}
	return false
}

func TestDNSSECOffAddsNoDO(t *testing.T) {
	srv, _, sawDO := dnssecProxy(t, "", true, false)
	resp := query(t, srv.UDPAddr().String(), "www.com", dns.TypeA)
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) == 0 {
		t.Fatalf("rcode=%s answers=%d, want forwarded answer", dns.RcodeToString[resp.Rcode], len(resp.Answer))
	}
	if sawDO.Load() {
		t.Fatal("mode off must not add DO to upstream queries")
	}
	if st := srv.DNSSECStats(); st.Mode != "off" || st.Secure+st.Insecure+st.Bogus+st.Indeterminate != 0 {
		t.Fatalf("mode off must not validate, got %+v", st)
	}
}

func TestDNSSECEnforceSecureFlow(t *testing.T) {
	srv, _, sawDO := dnssecProxy(t, "enforce", true, false)
	addr := srv.UDPAddr().String()

	// A DO client gets the signed answer with a trustworthy AD bit.
	resp := queryDO(t, addr, "www.com", dns.TypeA)
	if resp.Rcode != dns.RcodeSuccess || !resp.AuthenticatedData || !hasRRSIG(resp) {
		t.Fatalf("DO client: rcode=%s ad=%v rrsig=%v, want NOERROR+AD+RRSIG",
			dns.RcodeToString[resp.Rcode], resp.AuthenticatedData, hasRRSIG(resp))
	}
	if !sawDO.Load() {
		t.Fatal("upstream query should carry DO")
	}
	if st := srv.DNSSECStats(); st.Secure != 1 {
		t.Fatalf("secure = %d, want 1", st.Secure)
	}

	// A plain client gets the same answer stripped: no RRSIG, no AD.
	plain := query(t, addr, "www.com", dns.TypeA)
	if plain.Rcode != dns.RcodeSuccess || plain.AuthenticatedData || hasRRSIG(plain) {
		t.Fatalf("plain client: rcode=%s ad=%v rrsig=%v, want NOERROR stripped",
			dns.RcodeToString[plain.Rcode], plain.AuthenticatedData, hasRRSIG(plain))
	}

	// The DO client's second query rides the cache and keeps AD; the
	// cache serves the stored verdict, it never revalidates.
	secureBefore := srv.DNSSECStats().Secure
	cached := queryDO(t, addr, "www.com", dns.TypeA)
	if !cached.AuthenticatedData || !hasRRSIG(cached) {
		t.Fatalf("cached DO answer lost AD or records: ad=%v rrsig=%v",
			cached.AuthenticatedData, hasRRSIG(cached))
	}
	if after := srv.DNSSECStats().Secure; after != secureBefore {
		t.Fatalf("cache hit revalidated: secure %d -> %d", secureBefore, after)
	}
}

func TestDNSSECEnforceBogusIsServfail(t *testing.T) {
	srv, qlog, _ := dnssecProxy(t, "enforce", true, true)
	resp := queryDO(t, srv.UDPAddr().String(), "www.com", dns.TypeA)
	if resp.Rcode != dns.RcodeServerFailure {
		t.Fatalf("rcode = %s, want SERVFAIL for bogus answer", dns.RcodeToString[resp.Rcode])
	}
	if st := srv.DNSSECStats(); st.Bogus != 1 {
		t.Fatalf("bogus = %d, want 1", st.Bogus)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if recent := qlog.Recent(1); len(recent) == 1 {
			e := recent[0]
			if e.Verdict != querylog.VerdictBlocked || e.List != "dnssec" || e.Rule == "" {
				t.Fatalf("logged entry = %+v, want blocked by dnssec with a reason", e)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("query log entry never arrived")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestDNSSECPermissiveBogusPasses(t *testing.T) {
	srv, _, _ := dnssecProxy(t, "permissive", true, true)
	resp := queryDO(t, srv.UDPAddr().String(), "www.com", dns.TypeA)
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) == 0 {
		t.Fatalf("rcode=%s answers=%d, want the answer to pass in permissive mode",
			dns.RcodeToString[resp.Rcode], len(resp.Answer))
	}
	if resp.AuthenticatedData {
		t.Fatal("a bogus answer must never carry AD")
	}
	if st := srv.DNSSECStats(); st.Bogus != 1 {
		t.Fatalf("bogus = %d, want 1", st.Bogus)
	}
}

func TestDNSSECEnforceIndeterminatePasses(t *testing.T) {
	// An upstream that supplies no DNSSEC records at all: nothing can be
	// judged, and enforce mode must degrade to visibility, not outage.
	srv, _, _ := dnssecProxy(t, "enforce", false, false)
	resp := query(t, srv.UDPAddr().String(), "www.com", dns.TypeA)
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) == 0 {
		t.Fatalf("rcode=%s answers=%d, want indeterminate answers to pass",
			dns.RcodeToString[resp.Rcode], len(resp.Answer))
	}
	if st := srv.DNSSECStats(); st.Indeterminate != 1 {
		t.Fatalf("indeterminate = %d, want 1", st.Indeterminate)
	}
}

func TestDNSSECChainTypeQueriesDoNotSelfDeadlock(t *testing.T) {
	// A validated client query for DNSKEY/DS is the validator's own
	// chain food: judging it would fetch the same dedup key and wait on
	// itself until the context expired. These types skip judgment.
	srv, _, _ := dnssecProxy(t, "enforce", true, false)
	addr := srv.UDPAddr().String()
	// (The root itself can't be asked through handle() — NormalizeDomain
	// refuses the empty name — so zone-cut queries are the live cases.)
	for _, q := range []struct {
		name  string
		qtype uint16
	}{{"com.", dns.TypeDNSKEY}, {"com.", dns.TypeDS}} {
		began := time.Now()
		resp := queryDO(t, addr, q.name, q.qtype)
		if took := time.Since(began); took > 2*time.Second {
			t.Fatalf("%s/%s took %v — self-deadlocked", q.name, dns.TypeToString[q.qtype], took)
		}
		if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) == 0 {
			t.Fatalf("%s/%s: rcode=%s answers=%d, want the chain record",
				q.name, dns.TypeToString[q.qtype], dns.RcodeToString[resp.Rcode], len(resp.Answer))
		}
	}
}

func TestDNSSECModeSwitchesLive(t *testing.T) {
	srv, _, sawDO := dnssecProxy(t, "", true, false)
	addr := srv.UDPAddr().String()
	query(t, addr, "www.com", dns.TypeA)
	if sawDO.Load() {
		t.Fatal("mode off must not add DO")
	}

	cfg := config.Default()
	cfg.DNS.Listen = "127.0.0.1:0"
	cfg.DNS.Upstreams = srvUpstreams(srv)
	cfg.DNS.DNSSEC = "permissive"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := srv.ApplyConfig(cfg); err != nil {
		t.Fatal(err)
	}

	query(t, addr, "www.com", dns.TypeA)
	if !sawDO.Load() {
		t.Fatal("permissive mode should add DO upstream")
	}
	if st := srv.DNSSECStats(); st.Mode != "permissive" || st.Secure != 1 {
		t.Fatalf("stats after live switch = %+v, want permissive with 1 secure", st)
	}
}

// srvUpstreams rebuilds the upstream config from the running forward
// table, so a test config swap keeps pointing at the same stub.
func srvUpstreams(s *Server) []config.Upstream {
	var out []config.Upstream
	for _, up := range s.fwd.Load().defaults {
		out = append(out, config.Upstream{Address: up.Name(), Protocol: "udp"})
	}
	return out
}
