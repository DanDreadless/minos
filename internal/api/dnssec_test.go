package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"minos/internal/dnsproxy"
	"minos/internal/querylog"
)

// dnssecProxyStats is a ProxyStatsSource whose DNSSEC mode the test picks;
// "off" must make the API omit the block entirely.
type dnssecProxyStats struct{ mode string }

func (dnssecProxyStats) CacheStats() (uint64, uint64, int64, bool) { return 0, 0, 0, true }
func (dnssecProxyStats) UpstreamStats() []dnsproxy.UpstreamStat    { return nil }
func (d dnssecProxyStats) DNSSECStats() dnsproxy.DNSSECStat {
	return dnsproxy.DNSSECStat{Mode: d.mode, Secure: 9, Insecure: 2, Bogus: 4, Indeterminate: 1}
}

// recordAndWait records entries and blocks until the async writer has put
// the last one in the ring, so aggregates see them.
func recordAndWait(t *testing.T, qlog *querylog.Log, entries ...querylog.Entry) {
	t.Helper()
	for _, e := range entries {
		if e.Time.IsZero() {
			e.Time = time.Now()
		}
		qlog.Record(e)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(qlog.Recent(len(entries))) < len(entries) {
		if time.Now().After(deadline) {
			t.Fatal("query log entries never reached the ring")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStatusDNSSECBlock(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mode    string
		present bool
	}{
		{"permissive is reported", "permissive", true},
		{"enforce is reported", "enforce", true},
		{"off is omitted", "off", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestServer(t, "")
			s.cache = dnssecProxyStats{mode: tc.mode}
			rec := doJSON(t, s.Router(), http.MethodGet, "/api/status", "", nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			var got struct {
				DNSSEC *dnssecStatus `json:"dnssec"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if !tc.present {
				if got.DNSSEC != nil {
					t.Fatalf("dnssec = %+v, want omitted when validation is off", got.DNSSEC)
				}
				return
			}
			if got.DNSSEC == nil {
				t.Fatal("dnssec block missing, want it reported")
			}
			want := dnssecStatus{Mode: tc.mode, Secure: 9, Insecure: 2, Bogus: 4, Indeterminate: 1}
			if *got.DNSSEC != want {
				t.Fatalf("dnssec = %+v, want %+v", *got.DNSSEC, want)
			}
		})
	}
}

// A nil proxy (validation unwired) must not fabricate a DNSSEC block.
func TestStatusDNSSECAbsentWithoutProxy(t *testing.T) {
	s, _ := newTestServer(t, "")
	s.cache = nil
	rec := doJSON(t, s.Router(), http.MethodGet, "/api/status", "", nil)
	var got struct {
		DNSSEC *dnssecStatus `json:"dnssec"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.DNSSEC != nil {
		t.Fatalf("dnssec = %+v, want omitted with no proxy wired", got.DNSSEC)
	}
}

// The windowed would-block figure counts only DNSSEC audit marks — not
// enforced blocks, not other audit lists, not ordinary allows. Getting
// this wrong would inflate the number the dashboard uses to talk an
// operator into enabling enforce.
func TestStatsDNSSECWouldBlock(t *testing.T) {
	s, _ := newTestServer(t, "")
	s.cache = dnssecProxyStats{mode: "permissive"}
	recordAndWait(t, s.qlog,
		querylog.Entry{Client: "10.0.0.1", QName: "bad.example", Verdict: querylog.VerdictAllowed,
			AuditList: "dnssec", AuditRule: "signature verification failed"},
		querylog.Entry{Client: "10.0.0.1", QName: "bad.example", Verdict: querylog.VerdictAllowed,
			AuditList: "dnssec", AuditRule: "signature verification failed"},
		querylog.Entry{Client: "10.0.0.2", QName: "other.example", Verdict: querylog.VerdictAllowed,
			AuditList: "dnssec", AuditRule: "no DNSKEY for example matches its DS records"},
		// Noise that must not be counted.
		querylog.Entry{Client: "10.0.0.3", QName: "strict.example", Verdict: querylog.VerdictAllowed,
			AuditList: "hagezi-pro", AuditRule: "||strict.example^"},
		querylog.Entry{Client: "10.0.0.4", QName: "forged.example", Verdict: querylog.VerdictBlocked,
			List: "dnssec", Rule: "signature verification failed"},
		querylog.Entry{Client: "10.0.0.5", QName: "fine.example", Verdict: querylog.VerdictAllowed},
	)

	rec := doJSON(t, s.Router(), http.MethodGet, "/api/stats?hours=24", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		DNSSEC *dnssecAudit `json:"dnssec"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.DNSSEC == nil {
		t.Fatal("stats dnssec block missing while validation is on")
	}
	if got.DNSSEC.WouldBlock != 3 {
		t.Fatalf("would_block = %d, want 3 (only audit_list=dnssec rows)", got.DNSSEC.WouldBlock)
	}
	if len(got.DNSSEC.TopDomains) != 2 {
		t.Fatalf("top_domains = %+v, want 2 distinct domains", got.DNSSEC.TopDomains)
	}
	if d := got.DNSSEC.TopDomains[0]; d.QName != "bad.example" || d.Count != 2 {
		t.Fatalf("top domain = %+v, want bad.example x2 first", d)
	}
}

func TestStatsDNSSECOmittedWhenOff(t *testing.T) {
	s, _ := newTestServer(t, "")
	s.cache = dnssecProxyStats{mode: "off"}
	recordAndWait(t, s.qlog,
		querylog.Entry{Client: "10.0.0.1", QName: "bad.example", Verdict: querylog.VerdictAllowed,
			AuditList: "dnssec", AuditRule: "signature verification failed"},
	)
	rec := doJSON(t, s.Router(), http.MethodGet, "/api/stats", "", nil)
	var got struct {
		DNSSEC *dnssecAudit `json:"dnssec"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.DNSSEC != nil {
		t.Fatalf("dnssec = %+v, want omitted when validation is off", got.DNSSEC)
	}
}
